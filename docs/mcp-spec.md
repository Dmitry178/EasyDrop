# Model Context Protocol (MCP) Server Specification

Transport: stdio via the same binary – `easydrop mcp-server` (see `docs/cli-spec.md §1.7`).
SDK: official `modelcontextprotocol/go-sdk` (locked, see ARCHITECTURE.md §5).
Single binary, no separate `easydrop-mcp` executable.

> This document specifies the interface only. Design rationale, closed decisions
> and implementation history live in [`docs/implementation.md`](implementation.md).

### 0.1. Process environment (locked)
The server is a **spawned child process**: the client chooses its environment, not
the user's shell. Two consequences are contractual, because both produce failures
that look like easydrop bugs rather than configuration:

- **The working directory** is the client's, not the terminal's. Every tool
  resolves `config_path` relative to it, and that config's directory is the
  workspace that gets shipped (§1 tools).
- **`SSH_AUTH_SOCK` must be forwarded when ssh-agent is the auth method.** Auth is
  assembled in the order key file → agent → password (`SSHExecutor.authMethods`).
  A client that starts the server without `SSH_AUTH_SOCK` (or with it pointing at
  an agent unreachable from the child) loses the agent method. The reason is
  carried into the error rather than degrading to a bare `no ssh auth methods`,
  which sends the user looking in the wrong place: `SSH_AUTH_SOCK=<path> is set
  but unreachable … If easydrop was started by another program (an MCP client),
  that program must pass SSH_AUTH_SOCK through to its environment`. A dead socket
  never blocks the password or key-file methods – it only sharpens the diagnostic
  when nothing else is configured.
- **`EASYDROP_VAULT_PASSWORD`** is the same class of requirement for
  `manage_server` (§1.7): non-interactive, no prompt, fails closed.

## 1. JSON-RPC Tools Schema

AI assistants invoke these structured primitives to safely manage infrastructure configurations. The underlying MCP server pipes these method parameters directly into targeted execution routines within the `internal/core` logic layer.

### 1.0. Tool annotations
Every tool declares `annotations` explicitly (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`, `title`). The protocol defaults are `readOnly=false, destructive=true` – leaving them unset would misdescribe `get_status`/`get_logs` as mutating, which is exactly the signal a client uses to decide whether it may answer a question without asking the user first.

| Tool | `readOnlyHint` | `destructiveHint` | `idempotentHint` |
|------|----------------|-------------------|------------------|
| `init_project` | `false` | `true` | `true` |
| `deploy_app` | `false` | `false` | `true` |
| `get_status` | `true` | – (omitted: meaningless when read-only) | – |
| `get_logs` | `true` | – | – |
| `rollback_app` | `false` | `true` | `false` |
| `teardown_app` | `false` | `true` | `true` |
| `manage_server` | `false` | `true` | `true` |

Notes on the non-obvious rows:
- **`deploy_app` is not destructive.** It replaces the running container but keeps a rollback backup under Blue-Green and never deletes data, so it is additive. It is still not free (minutes of wall clock, previous image dropped) – the description says so.
- **`rollback_app` is not idempotent.** It *consumes* the single backup; a second call fails until the next replacing deploy. Marking it idempotent would let a client retry it blindly.
- **`init_project` is destructive** because `force=true` regenerates the file from scratch and discards hand edits. Without `force` it is a no-op on an existing file.
- **`manage_server` is destructive** in the local store sense: `remove` forgets the credentials for a host, recoverable only by re-entering them.
- **`destructiveHint` is omitted on the read-only tools** – the spec scopes it to `readOnlyHint == false`, so a `false` there is noise.
- Every destructive tool's **description tells the agent to confirm with the user**. The hint alone does not stop a model, and the description is what it actually reads. Covered by a test over a real session.
- These hints are **not a security boundary** – per the MCP spec, clients must not make authorization decisions from annotations received from a server.

### 1.1. `init_project`
- **Description:** Scan the project and generate `easydrop.toml`: detect the listen port (Dockerfile `EXPOSE`/`CMD`, compose ports, an explicit `--port` flag, `PORT=` in `.env`, framework conventions, or a bounded look at the project's own source), the stack, and the driver. Nothing is guessed – when no port can be found, `[app].port` is left unset and `deploy_app` fails until you set it, so either resolve it by reading the code or pass the port via the `port` argument.
- **Detection:** identical rules to CLI `init` (see `docs/cli-spec.md` §1.1). The result names the **source** of every detected value, and when no port is found the key is simply absent – `deploy_app` then fails with an actionable message instead of deploying a guess.
- **Result text is the CLI's text:** the tool returns `ScaffoldResult.Report()` verbatim – the same string `easydrop init` prints. There is exactly one report, so an agent cannot receive a weaker description of the outcome than a human. The port-missing block therefore carries the **method** (where the server binds), the obligation to write `app.port` into `easydrop.toml` – easydrop has no tool for that – and both escape hatches, instead of only forbidding a guess.
- **The description matters:** it is always in the model's context, unlike the result, which only arrives after the call. That is why the port caveat lives there too. Both the description and the argument are covered by tests over a real session.
- **Arguments:**
  - `force` (boolean, optional): Overwrite an already existing `easydrop.toml`. Regenerates it from scratch, discarding hand edits. Defaults to `false`. (Mirrors CLI `init --force`.)
  - `port` (integer, optional): The port the app listens on **inside** the container. Overrides detection and is reported as `explicit override`; omitted (or `0`) means "detect". Must be 1-65535. This is the supported path for an agent that determined the port itself. (Mirrors CLI `init --port`.)

### 1.2. `deploy_app`
- **Description:** Spawns the entire execution pipeline covering target environment health checks, workspace asset compiling, and live ingress container switches.
- **Arguments:**
  - `config_path` (string, optional): Specific file track targeting the destination deployment blueprint. Defaults to `./easydrop.toml`.
  - `no_cache` (boolean, optional): Bypass the Docker build cache for this run (overrides `build.no_cache`, mirrors CLI `deploy --no-cache`). Defaults to `false`.
  - `blue_green` (boolean, optional): Enable zero-downtime Blue-Green deployment for this run (overrides `driver.blue_green`, mirrors CLI `deploy --blue-green`). Defaults to `false` (direct in-place redeploy).

### 1.3. `get_status`
- **Description:** Pulls operational system metrics, active container processes, and reverse proxy properties from the destination environment.
- **`ssl_status` describes the certificate nginx is actually serving.** For an `Up` app with `nginx.ssl` and a `domain`, this is what closes the question an agent most wants answered after an HTTPS deploy: *is it really serving TLS?*
  - **It is read from the running system, never from the config.** `nginx -T` gives the loaded configuration and the vhost names the certificate; easydrop reads and parses that file. Config says what was requested, this says what is loaded, and the two diverge exactly when certbot has rewritten the vhost, when something else removed it, or when a certificate could not be issued – so reporting the configured intent would be a claim easydrop cannot back.
  - **Every fact comes from that one file**, which makes a self-signed and a Let's Encrypt certificate behave identically: `self-signed, expires YYYY-MM-DD` or `TLS, expires YYYY-MM-DD`, suffixed `EXPIRING` once less than 30 days remain, or `… – DOES NOT COVER <domain>` when the certificate does not name the host it is served under.
  - **The kind is read from the certificate**, not from `nginx.self_signed`: issuer equal to subject means self-signed. Reading the config key would let a domain re-issued by certbot, or a config switched since the last deploy, be mislabelled.
  - **An absent value means "could not tell"** – no vhost for this domain, TLS not served for it, or nginx unreadable – and must **not** be read as "no TLS".
  - Not reported for a downed app, where it would imply the deployment is healthy.
- **Arguments:**
  - `app_name` (string, required): Target application name metadata identifier used to extract landscape scopes.

### 1.4. `get_logs`
- **Description:** Reads current remote stdout/stderr application output streams for immediate debugging or error root-cause diagnostics. Backed by `deploy.Logs(ctx, appName, LogOptions)` over the driver's `Logs(ctx, appName, lines, follow)` channel – MCP collects the channel to a list (streaming `follow` is supported; the server closes the stream on client cancel).
- **Log filtering:** `filter` is a **case-insensitive regular expression** applied to the collected lines, and it exists for the agent's benefit, not the human's: an unfiltered snapshot lands verbatim in the context window, so "stream the logs and tell me why the database container is failing" is answered with 2000 lines of noise. Three supporting rules make the result unambiguous rather than merely smaller:
  - **A filtered read scans a wider window.** With `tail` omitted, `filter` set raises the default from 50 to 1000 lines. A narrow window returns "0 matched", which a model reads as "no errors" rather than "look further back".
  - **An invalid expression is an error result** (`isError`, naming the bad pattern), never a filter that silently passes everything through – the latter would be indistinguishable from a healthy container.
  - **Counts are reported.** `scanned` and `matched` accompany `lines`, and an empty read returns an explanatory line (`no log lines available` vs `no lines matched (scanned N lines …)`) instead of an empty string.
- **Output bound:** `follow` collection is capped at 2000 kept lines (`followLogCap`); the text payload is additionally truncated at 400 matched lines with an explicit `[easydrop] output truncated: …` marker, so a long stream cannot silently truncate. (Mirrors CLI `logs -g`.)
- **Arguments:**
  - `app_name` (string, required): Targeted application name identifier.
  - `tail` (integer, optional): Lines of history to **scan**, not to return. Defaults to 50, or 1000 when `filter` is set.
  - `follow` (boolean, optional): Keep streaming new lines (mirrors CLI `logs --follow`). Defaults to `false`.
  - `filter` (string, optional): Case-insensitive regular expression; only matching lines are returned. E.g. `"error|panic|fatal|traceback"`. Defaults to empty (no filtering).

### 1.5. `rollback_app`
- **Description:** Restores the stopped backup kept by the last Blue-Green deploy (mirrors CLI `rollback`). Backed by `Rollback(ctx, app)`. Fails fast with "no rollback backup" when no backup exists – without touching anything.
- **Arguments:**
  - `app_name` (string, required): Targeted application name identifier.
  - `config_path` (string, optional): Deployment blueprint for ports/healthcheck/ingress settings. Defaults to `./easydrop.toml`.

### 1.6. `teardown_app`
- **Description:** Removes the deployment from the target host (mirrors CLI `teardown`). Backed by `deploy.Teardown`: the driver's `Teardown` removes containers (including the Blue-Green backup and leftover `-green`), plus the compose/swarm stack and its state dir; then the **managed nginx vhost is removed and nginx reloaded** when `nginx.domain` is set. **Named volumes are never deleted**, nor is the built image (it may be shared), nor any Let's Encrypt certificate (certbot owns it). Idempotent – tearing down an app that is not deployed is not an error.
- **Vhost removal is part of the default, not an opt-in.** An agent reasoning about "the app is gone" must know the domain stops answering too. Left behind, nginx keeps proxying to a port with no container on it and the domain returns `502` instead of refusing the connection – the deployment looks half-alive. easydrop wrote both files, so it owns exactly them.
- **Order and safety:** containers first (a failure there aborts with the host untouched), then the vhost files, then `sudo nginx -t`, and nginx is reloaded only if that test passes – reloading a rejected config would take down every other site on the host. When the test fails the tool reports `ingress_reloaded: false` with a note, because the running nginx still serves the old config and the domain keeps answering `502` until someone reloads. The agent must relay that rather than reporting a clean teardown.
- **Arguments:**
  - `app_name` (string, required): Targeted application name identifier.
  - `config_path` (string, optional): Deployment blueprint (selects the driver and target host). Defaults to `./easydrop.toml`.
  - `purge` (boolean, optional): Also delete the self-signed certificate and key for `nginx.domain`. Defaults to `false`. A Let's Encrypt certificate is **never** deleted whatever this is set to – certbot renews it on its own timer and may still be serving the domain through a vhost easydrop never wrote.

### 1.7. `manage_server`
- **Description:** Mutates host records in EasyDrop's local server store to safely bind, evaluate, or deprecate environment access configurations.
- **Persistence (locked):** encrypted vault at `~/.easydrop/servers.vault` – AES-256-GCM with a scrypt-derived key (`N=32768, r=8, p=1`), dir `0700`, file `0600`. The key comes **only** from `EASYDROP_VAULT_PASSWORD`; MCP is non-interactive, so there is no prompt and no fallback – the tool fails closed. A legacy plaintext `servers.toml` is imported once and renamed `servers.toml.migrated`. `EASYDROP_SERVERS_FILE` overrides the path. Never log or echo secrets.
- **Arguments:**
  - `action` (string, required): Operational mutating intent flag. Permitted settings strictly match: `"add"` or `"remove"`.
  - `config` (object, required): Explicit server properties containing credential attributes (`host`, `user`, and optionally matching `ssh_key` or `password`).

## 2. Exported Resources Schema
The protocol maps contextual state metrics allowing connected LLM instances to autonomously query baseline environment criteria:
- `easydrop://docs/schema`: Active structural JSON Schema validation blueprint enforcing correct structural rules on `easydrop.toml` parameters. **Generated by reflection over `internal/models.Config`** – descriptions/defaults/enums come from a `schemaMeta` table, and a config field without a meta entry is a hard error, so the published schema can never drift from the parser. The schema describes **types**; `${VAR}` interpolation happens later, at `ParseConfig` time, and a `password` field typed as a string may legitimately hold `"${EASYDROP_SSH_PASSWORD}"`.
- `easydrop://docs/troubleshooting`: Systemic operational runbook compiling standard infrastructure mitigation paths (such as port conflicts, failing container network probes, missing proxy headers, or a busy host port) for automated self-healing execution loops. Read it before improvising a fix: the deploy-time failures it covers are the ones an agent cannot diagnose from the error text alone.

### 2.1. TLS on local targets
Let's Encrypt cannot validate a local endpoint, so `nginx.ssl = true` requires a different route on `localhost` and on internal DNS names. `nginx.ssl = true` without a `domain`, or with `nginx.self_signed` unset, is rejected at parse time rather than quietly ignored – an agent that read the config, saw `ssl = true`, deployed, and reported "TLS is in place" must not be able to do so.

The supported answer is `nginx.self_signed = true` alongside a `domain` (`"localhost"` or an internal name). EasyDrop then generates the certificate itself, installs it, and serves `:443`, so the agent's claim about HTTPS is true. The `easydrop://docs/schema` resource carries the key, so an agent discovers the option from the same source the parser uses.

## 3. Tool → Core mapping
| Tool | Core entrypoint |
|------|-----------------|
| `init_project` | config scaffolding (CLI `init`, shared report, `port` override) |
| `deploy_app` | `Bootstrapper.Bootstrap` → builder `Build(ctx, app)` → `SingleDriver.Deploy(ctx, app)` → scheme-aware probe → Nginx/Certbot |
| `get_status` | `SingleDriver.Status(ctx, appName)` |
| `get_logs` | `deploy.Logs(ctx, appName, LogOptions)` → `SingleDriver.Logs(ctx, appName, lines, follow)` + `collectLogs` filter |
| `rollback_app` | `SingleDriver.Rollback(ctx, app)` |
| `teardown_app` | `deploy.Teardown(ctx, appName, TeardownOptions{Purge})` → driver `Teardown` + `NginxManager.RemoveIngress` + `SelfSignedCertifier.RemoveCert` |
| `manage_server` | encrypted vault (`~/.easydrop/servers.vault`, scrypt + AES-256-GCM) |
