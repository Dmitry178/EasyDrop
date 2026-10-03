# Model Context Protocol (MCP) Server Specification

Transport: stdio via the same binary – `easydrop mcp-server` (see `docs/cli-spec.md §1.7`).
SDK: official `modelcontextprotocol/go-sdk` (locked, see ARCHITECTURE.md §5).
Single binary, no separate `easydrop-mcp` executable.

## 1. JSON-RPC Tools Schema

AI assistants invoke these structured primitives to safely manage infrastructure configurations. The underlying MCP server pipes these method parameters directly into targeted execution routines within the `internal/core` logic layer.

### 1.0. Tool annotations (M16, locked)
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
- **Detection (M14, FR-02/FR-02B):** identical rules to CLI `init` (see `docs/cli-spec.md` §1.1). The result names the **source** of every detected value, and when no port is found the key is simply absent (OD-04) – `deploy_app` then fails with an actionable message instead of deploying a guess.
- **Result text is the CLI's text (M15):** the tool returns `ScaffoldResult.Report()` verbatim – the same string `easydrop init` prints. There is exactly one report, so an agent cannot receive a weaker description of the outcome than a human. The port-missing block therefore carries the **method** (where the server binds), the obligation to write `app.port` into `easydrop.toml` – easydrop has no tool for that – and both escape hatches, instead of only forbidding a guess.
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
- **Arguments:**
  - `app_name` (string, required): Target application name metadata identifier used to extract landscape scopes.

### 1.4. `get_logs`
- **Description:** Reads current remote stdout/stderr application output streams for immediate debugging or error root-cause diagnostics. Backed by `deploy.Logs(ctx, appName, LogOptions)` over the driver's `Logs(ctx, appName, lines, follow)` channel – MCP collects the channel to a list (streaming `follow` is supported; the server closes the stream on client cancel).
- **Log filtering (M16):** `filter` is a **case-insensitive regular expression** applied to the collected lines, and it exists for the agent's benefit, not the human's: an unfiltered snapshot lands verbatim in the context window, so "stream the logs and tell me why the database container is failing" is answered with 2000 lines of noise. Three supporting rules make the result unambiguous rather than merely smaller:
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
- **Description:** Removes the deployment from the target host (mirrors CLI `teardown`). Backed by `Teardown` on the configured driver: containers (including the Blue-Green backup and leftover `-green`), plus the compose/swarm stack and its state dir. **Named volumes are never deleted.** Idempotent – tearing down an app that is not deployed is not an error.
- **Arguments:**
  - `app_name` (string, required): Targeted application name identifier.
  - `config_path` (string, optional): Deployment blueprint (selects the driver and target host). Defaults to `./easydrop.toml`.

### 1.7. `manage_server`
- **Description:** Mutates host records in EasyDrop's local server store to safely bind, evaluate, or deprecate environment access configurations.
- **Persistence (locked):** encrypted vault at `~/.easydrop/servers.vault` – AES-256-GCM with a scrypt-derived key (`N=32768, r=8, p=1`), dir `0700`, file `0600`. The key comes **only** from `EASYDROP_VAULT_PASSWORD`; MCP is non-interactive, so there is no prompt and no fallback – the tool fails closed. A legacy plaintext `servers.toml` is imported once and renamed `servers.toml.migrated`. `EASYDROP_SERVERS_FILE` overrides the path. Never log or echo secrets (NFR-02).
- **Arguments:**
  - `action` (string, required): Operational mutating intent flag. Permitted settings strictly match: `"add"` or `"remove"`.
  - `config` (object, required): Explicit server properties containing credential attributes (`host`, `user`, and optionally matching `ssh_key` or `password`).

## 2. Exported Resources Schema
The protocol maps contextual state metrics allowing connected LLM instances to autonomously query baseline environment criteria:
- `easydrop://docs/schema`: Active structural JSON Schema validation blueprint enforcing correct structural rules on `easydrop.toml` parameters. **Generated by reflection over `internal/models.Config`** – descriptions/defaults/enums come from a `schemaMeta` table, and a config field without a meta entry is a hard error, so the published schema can never drift from the parser. The schema describes **types**; `${VAR}` interpolation (M13) happens later, at `ParseConfig` time, and a `password` field typed as a string may legitimately hold `"${EASYDROP_SSH_PASSWORD}"`.
- `easydrop://docs/troubleshooting`: Systemic operational runbook compiling standard infrastructure mitigation paths (such as port conflicts, failing container network probes, or missing proxy headers) for automated self-healing execution loops.

## 3. Tool → Core mapping
| Tool | Core entrypoint |
|------|-----------------|
| `init_project` | config scaffolding (Milestone 7, FR-02; detection M14, shared report + `port` override M15) |
| `deploy_app` | `Bootstrapper.Bootstrap` → builder `Build(ctx, app)` → `SingleDriver.Deploy(ctx, app)` → Nginx/Certbot |
| `get_status` | `SingleDriver.Status(ctx, appName)` |
| `get_logs` | `deploy.Logs(ctx, appName, LogOptions)` → `SingleDriver.Logs(ctx, appName, lines, follow)` + `collectLogs` filter (M16) |
| `rollback_app` | `SingleDriver.Rollback(ctx, app)` |
| `teardown_app` | `Teardown(ctx, appName)` on the configured driver |
| `manage_server` | encrypted vault (`~/.easydrop/servers.vault`, scrypt + AES-256-GCM) |
