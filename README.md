# EasyDrop

**English** · [Russian](README_RU.md)

**EasyDrop** is a lightweight CLI tool and Go-based MCP server designed to automate Docker application deployment to any Linux environment (localhost, VPS, bare-metal, or Raspberry Pi). 

It delivers a PaaS-like experience (similar to Railway or Fly.io) on your own infrastructure by combining an orchestrator, a reverse proxy (Nginx), and automated SSL certificate management (Certbot) into a single binary.

---

## Key Features

*   **One Core, Two Interfaces:** A unified deployment core accessible via either a developer-friendly terminal CLI or an MCP server for AI agents.
*   **Zero-Config Reverse Proxy:** Automatically generates Nginx configurations and provisions Let's Encrypt SSL certificates right out of the box.
*   **Remote & Local Builds:** Build Docker images directly on the target host (no external registry required) or locally and push to a private/public registry – the target then pulls the image instead of receiving your source.
*   **Zero-to-Hero Bootstrapping:** Automatically inspects and prepares clean Linux servers by installing Docker Engine, Docker Compose, and configuring firewall rules.
*   **AI-Native (MCP):** Native Model Context Protocol integration allows Cursor, Claude Code, and other AI assistants to manage infrastructure via natural language.

---

## Installation

### From source (recommended)

Requires Go >= 1.27 (see `go.mod`; `GOTOOLCHAIN=auto` resolves it).

```bash
git clone https://github.com/Dmitry178/EasyDrop easydrop && cd easydrop
make install          # builds, then installs into ~/.local/bin (no sudo)
easydrop --version    # easydrop version 1.0.0
```

`make install` runs the same build as `make build` and copies the result to
`$(PREFIX)/bin` – `~/.local/bin` by default, a directory that is normally
already on `PATH`. If it is not, the target prints the exact line to add. For a
system-wide install:

```bash
sudo make install PREFIX=/usr/local     # -> /usr/local/bin/easydrop
make uninstall                          # remove it again
```

Upgrading later is `git pull && make install`. The version stamped into the
binary comes from `git describe`: a short commit hash on a clean tree, with a
`-dirty` suffix while you have uncommitted changes, and the tag name itself once
you create one (`git commit && git tag v1.0.0` → `easydrop version v1.0.0`).
Override it per build with `make install VERSION=1.0.0`.

### Prebuilt binaries

`make release` cross-compiles linux/darwin/windows × amd64/arm64 into
`bin/dist/` with a `SHA256SUMS` manifest – handy for dropping a binary onto a
server that has no Go toolchain:

```bash
make release VERSION=1.0.0
ls bin/dist
# on the target machine:
sha256sum -c SHA256SUMS
install -m 0755 easydrop_linux_amd64 /usr/local/bin/easydrop
```

### Go-native install

```bash
go install ./cmd/easydrop      # -> $(go env GOBIN), or $(go env GOPATH)/bin
```

Note that `GOBIN`/`GOPATH/bin` is **not** on `PATH` on many setups, so
`make install` is usually the better choice.

### Without installing

The built binary works in place, which is handy for a quick check:

```bash
./bin/easydrop --version
make run ARGS="status"         # rebuild, then run
```

---

## Quick Start

### 1. Check the installation
```bash
easydrop --version
```
Prints the stamped version, e.g. `easydrop version v1.0.0`. Run it from any
directory – it needs no config file yet.

### 2. Initialize a Project
Run the following command in your project root directory:
```bash
easydrop init
```
The tool scans the directory and writes a tailored `easydrop.toml`: the listen
port from the Dockerfile `EXPOSE` (or an `nginx` final stage), the compose
`ports` mapping, `package.json` (explicit script port, else the framework's
default) or – for languages whose manifests carry no port, Go above all – a
bounded look at your own source, plus the detected stack and the driver implied
by a compose file. Every value is reported with its source, and nothing is ever
guessed:

```
wrote easydrop.toml
  app     go-svc (directory name)
  port    9090 (source scan)
  stack   go
  driver  single
```

Two things need you afterwards: `[server].host` (init writes `localhost`), and
– when nothing in the project states a port – `app.port`. In that case init
writes **no port at all** and prints `port NOT SET`:

```
  port    NOT SET – nothing in the project states it

ACTION REQUIRED: set [app].port in easydrop.toml …
```

There is deliberately no 8080 fallback. A wrong port builds fine and only breaks
the deploy – a healthcheck timeout for the single driver, or, for compose/swarm
(which have no HTTP probe), a deploy that *succeeds* behind a 502 proxy. An
incomplete config that refuses to deploy is far cheaper to debug than a complete
one that lies. Existing files are never overwritten; `--force` regenerates the
file from scratch, discarding hand edits.

### 3. Configure Your Environment
Edit the generated `easydrop.toml` to map your target server details and domain:

```toml
[app]
name = "my-awesome-api"
port = 8080             # port your app listens on INSIDE the container
# host_port = 18080     # optional: port published on the host (defaults to `port`)

[server]
host = "185.178.21.42" # Use "localhost" or "127.0.0.1" for local deployment
user = "root"
ssh_key = "~/.ssh/id_rsa"

[build]
strategy = "remote"        # "remote" = build on the host (no registry)
# strategy = "local"       # build here, push to `registry`, pull on the host
# registry = "ghcr.io/myorg"
# image = "web:2.1"        # optional name:tag for the pushed image

[driver]
type = "single"   # "single" (one container), "compose" (stack file), or "swarm"

[nginx]
domain = "my-project.com"
ssl = true
```

### 4. Deploy
Deploy your stack to production with a single command:
```bash
easydrop deploy
```
EasyDrop will handle the SSH handshake, bootstrap Docker if missing, securely transfer code, build the image, provision Nginx/SSL, and switch containers. The default `single` driver redeploys in place; add `--blue-green` for a zero-downtime swap (it keeps a rollback backup). Compose and Swarm projects deploy with the same command.

**Rolling back:** `easydrop rollback` restores the previous version – for Blue-Green deploys the stopped container backup, for Compose/Swarm the previous stack file.

### 5. Day-to-day commands

```bash
easydrop status                 # is the app up, since when, TLS status
easydrop logs -f                # stream container logs
easydrop logs -n 200            # last 200 lines and exit
easydrop rollback               # restore the previous Blue-Green backup
easydrop teardown               # remove containers, backups and the nginx vhost (volumes are kept)
easydrop teardown --purge       # ...and the self-signed certificate
easydrop deploy -c path/to/easydrop.toml    # any command takes an explicit config
```

`status`, `logs`, `rollback` and `teardown` read `app.name` from
`./easydrop.toml`; pass a name to override it (`easydrop logs my-app`). All
commands are documented in [docs/cli-spec.md](docs/cli-spec.md).

---

## Configuration Examples

Not sure which knobs to turn? The [`examples/`](examples/) directory holds a
ready-to-copy `easydrop.toml` for every supported scenario – one file per case,
each commented with *why* those values are what they are:

*   remote VPS over an SSH key or a password, hardened SSH ports
*   local development with a `host_port` split, and local TLS via a self-signed
    certificate
*   domain reverse proxy, with or without Let's Encrypt TLS
*   Blue-Green zero-downtime deploys and `--no-cache` rebuilds
*   build-here + registry push, including pinned image tags
*   Compose and Swarm stacks, with and without ingress

Read [`examples/README.md`](examples/README.md) for the annotated walkthrough of
all cases, the complete key reference and the validation rules. If you only want
the schema, start from
[`examples/99-full-reference.toml`](examples/99-full-reference.toml).

**Credentials:** a literal `server.password` is a secret in your repository.
Either let `ssh-agent` hold the key (`eval "$(ssh-agent -s)" && ssh-add <key>`,
then leave the credential fields empty), or interpolate it –
`password = "${EASYDROP_SSH_PASSWORD}"` resolved from the environment or a
git-ignored `.easydrop.env` next to the config. Unset variables fail before any
SSH connection, and those files are never shipped to the target host.

---

## Using with AI Assistants (MCP)

EasyDrop exposes a native **Model Context Protocol (MCP)** interface over
**stdio** – the same binary, one extra argument:

```bash
easydrop mcp-server
```

Point your AI client at that command and it can drive deployments for you. The
client spawns the binary itself, so the binary must be on `PATH` – or give the
absolute path (see the note at the end of this section).

Available tools: `deploy_app`, `get_status`, `get_logs`, `init_project`,
`rollback_app`, `teardown_app`, `manage_server`. Two resources are exposed as
well: `easydrop://docs/schema` (JSON Schema of `easydrop.toml`, generated from
the same structs the parser uses) and `easydrop://docs/troubleshooting` (a
self-healing runbook: ports, probes, proxy, snap-docker limits, sudo, rollback,
SSH). Full details in [docs/mcp-spec.md](docs/mcp-spec.md).

### Claude Code

Add it with the CLI (recommended – it writes the file for you):

```bash
claude mcp add --scope user    easydrop -- easydrop mcp-server   # all your projects
claude mcp add --scope project easydrop -- easydrop mcp-server   # this repo only
claude mcp list                                                   # verify
```

Or edit the file yourself. **Project scope** writes `.mcp.json` in the project
root (commit it, so the team shares the setup); **user scope** writes
`~/.claude.json`:

```json
{
  "mcpServers": {
    "easydrop": {
      "command": "easydrop",
      "args": ["mcp-server"]
    }
  }
}
```

### Cursor

Edit **`.cursor/mcp.json`** – in the project for a workspace-scoped server, or
`~/.cursor/mcp.json` for all projects. Cursor also exposes this in
*Settings → MCP & Integrations*, where you can add a stdio server by hand.

```json
{
  "mcpServers": {
    "easydrop": {
      "command": "easydrop",
      "args": ["mcp-server"]
    }
  }
}
```

### Codex

Codex reads TOML, not JSON – the file is `~/.codex/config.toml`:

```toml
[mcp_servers.easydrop]
command = "easydrop"
args = ["mcp-server"]
```

Or let the CLI write it:

```bash
codex mcp add easydrop -- easydrop mcp-server
codex mcp list
```

### opencode

Add the server to **`~/.config/opencode/opencode.json`** (all projects) or to
`opencode.json` in the project. Note that opencode's shape differs: the `mcp`
object, a required `type`, and `command` as an **array**:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "easydrop": {
      "type": "local",
      "command": ["easydrop", "mcp-server"],
      "enabled": true
    }
  }
}
```

opencode reads its config **once at startup** – quit and restart it after
editing. Project config wins over global config, and the two are deep-merged.

### Notes that apply to all four clients

* **Absolute paths.** If the client starts with a minimal environment and cannot
  see your `PATH`, replace `"easydrop"` with the real location –
  `command -v easydrop` prints it (typically `~/.local/bin/easydrop`, or
  `/usr/local/bin/easydrop` for a system-wide install). This is the single most
  common reason a configured server fails to start.
* **Restart the client** after changing the config (mandatory for opencode).
* **`manage_server`** is the only tool that needs a secret: the encrypted server
  vault is locked with `EASYDROP_VAULT_PASSWORD`, and there is no interactive
  prompt. Pass it through the client's env support, e.g.
  `claude mcp add -e EASYDROP_VAULT_PASSWORD=… --scope user easydrop -- easydrop mcp-server`,
  `--env` for `codex mcp add`, or the `environment` object in opencode's config.
  Everything else (`deploy_app`, `get_logs`, …) works with no configuration at
  all – credentials come from `easydrop.toml`, `ssh-agent` or `.easydrop.env`.
* **Path sensitivity.** Like the CLI, the tools operate on the working directory
  the client was started in, and the directory containing the `easydrop.toml`
  passed as `config_path` is the workspace that gets shipped.
* **`ssh-agent` needs to be inherited.** If you authenticate with an agent
  instead of a key file, the client must pass `SSH_AUTH_SOCK` into the MCP
  server's environment. AI clients often launch the server with a minimal
  environment where the variable is missing, or inherited but pointing at an
  agent that is not reachable from the spawned process – `easydrop deploy` works
  in your terminal while the same deploy through the assistant fails to
  authenticate. When that happens easydrop says so explicitly
  (`SSH_AUTH_SOCK=… is set but unreachable`) instead of a bare "no ssh auth
  methods". Three ways out, in order of preference:
  1. Put the key in the config – `ssh_key = "~/.ssh/id_ed25519"`. No agent, no
     environment, works in every client.
  2. Interpolate it: `password = "${EASYDROP_SSH_PASSWORD}"` read from
     `.easydrop.env`.
  3. Forward the variable through the client's env support, same mechanism as
     `EASYDROP_VAULT_PASSWORD` below – `env` in the Claude/Cursor JSON,
     `env = { … }` in opencode, `--env` for `codex mcp add`.

Once connected, ask your assistant things like:
> *"Deploy the current project to my server"*
> *"Stream the logs and let me know why the database container is failing"*

Every tool declares its behaviour through standard MCP tool annotations:
`get_status` and `get_logs` are marked read-only, while `rollback_app`,
`teardown_app`, `init_project {force:true}` and `manage_server` are marked
destructive. Clients that support confirmation use these to ask you before
acting, so the assistant can answer questions about your server without
touching it. Diagnosing a failing container is usually just
`get_status`, then `get_logs` with a filter:

```json
{"app_name": "my-api", "tail": 1000, "filter": "error|panic|fatal|traceback"}
```

The filter is a case-insensitive regular expression applied to the lines the
container produced; the result reports how many lines were scanned and how many
matched, so "0 matched" is never confused with "the container logged nothing".

---

## Scope and Limitations

EasyDrop is a **single-host deployment tool**, not an orchestrator. Knowing
where the edge is saves you from reaching for it in the wrong situation.

**A good fit**

*   One or a handful of apps on one server you already own (VPS, bare metal,
    Raspberry Pi).
*   MVP and pet projects, small production services, staging environments.
*   Replacing a hand-rolled `docker build && docker run && edit nginx.conf`
    script with something scripted, repeatable and AI-drivable.
*   Teams that want a Railway/Fly.io-style workflow on their own hardware, with
    the whole deployment in git.

**Not a fit** – reach for Kubernetes, Nomad, ECS or plain systemd units instead

*   Multi-node scheduling, replica autoscaling, rolling updates across hosts, or
    any cross-node failover. One `easydrop.toml` is one app on one host.
*   High availability. EasyDrop runs one app container behind Nginx on a single
    machine; if the machine is down, the app is down.
*   Canary or percentage traffic splitting, feature flags, or serving several
    versions of the same app at once.

**Hard limits, worth knowing before you rely on it**

*   **Zero-downtime is one release deep.** A Blue-Green deploy stages a second
container on the `{host_port, host_port+1}` pair, health-checks it and flips
    the proxy. Both ports must be free (`app.host_port` is therefore capped at
    65534), there is no traffic splitting, and the previous release is kept as
    exactly one backup – the next Blue-Green deploy discards it. `rollback_app`
    consumes that backup and cannot be repeated. A port that is not free fails
    the deploy *before* anything is started, naming the port and the squatter,
    with production still serving.
*   **Direct deploys** (the default) restart the container in place: brief
    downtime, no backup, no rollback.
*   **Health checks are HTTP, optionally over TLS.** `app.health_check_path` must
    answer `200` from inside the container, probed up to 10 times 2 seconds
    apart. By default the probe tries HTTP and falls back to HTTPS in the same
    attempt, so an app that answers only over TLS – or that redirects everything
    to HTTPS – deploys normally; `app.health_check_scheme` pins it to `http` or
    `https`. Redirects are never followed, deliberately: `-L` would let an app
    pass by bouncing the probe at an unrelated `200` page. There are no TCP,
    command or exec-style checks, and no per-app tuning of the retry count.
*   **Let's Encrypt is HTTP-01 only, and a self-signed alternative exists.** The
    ACME path needs the domain to resolve publicly with port 80 reachable; there
    is no DNS-01 challenge and no wildcard handling. EasyDrop writes a plain HTTP
    vhost and lets `certbot --nginx` add the TLS block, after which renewal is
    certbot's own timer, not something EasyDrop schedules.
    For `localhost` and internal DNS – where no ACME authority can validate you –
    set `nginx.self_signed = true`: EasyDrop generates a P-256 leaf with
    SubjectAltName, installs it in `/etc/nginx/ssl/` (key `0600`), and serves
    `:443` itself, keeping `:80` proxying. It is reused until it is within 30
    days of expiry, so the browser warning you accept once does not come back on
    every deploy. The certificate is **not** added to your system trust store –
    that needs sudo and would change what every other program on your machine
    trusts, so EasyDrop prints the command instead.
*   **nginx and certbot must already be installed** on the target. Bootstrapping
    installs Docker Engine, Docker Compose and the firewall rules – not the web
    server.
*   **Data is never backed up or migrated.** `teardown_app` deliberately keeps
    named volumes, but nothing here snapshots, replicates or restores a database.
    The built image also survives: `build.image` may be shared with another app,
    and easydrop cannot know who else it feeds.
*   **`teardown` removes the nginx vhost, and reloads nginx only if `nginx -t`
    passes.** Reloading a rejected config would take down every other site on
    the host, so if the test fails the files are gone but nginx keeps serving
    what it had loaded – the domain keeps answering `502` until someone reloads.
    That case is reported rather than hidden. A Let's Encrypt certificate is
    never deleted (certbot owns it); a self-signed one needs `--purge` /
    `purge: true`.
*   **`status` reports the certificate nginx actually serves, in both modes.**
    It reads the running configuration and the certificate file the vhost names,
    so the expiry date, the `EXPIRING` warning and a `DOES NOT COVER` mismatch
    are facts rather than the configured intent. No output means "could not
    tell", never "no TLS".
*   **Logs live in the Docker daemon.** `logs` / `get_logs` read them live with
    a tail bound and a filter; there is no shipping, retention or search. Pair it
    with your own log agent if you need history.
*   **There is no secrets manager.** `${VAR}` interpolation pulls values from the
    environment or a git-ignored `.easydrop.env`, but a literal
    `server.password` in the config is a secret in your repository.
*   **File-permission protection is POSIX-only.** On Linux and macOS the MCP
    server vault is written `0600` and the generated private key `0600`, and
    both are asserted by tests. **On Windows those bits do not exist**: Go writes
    every file as `0666` and access is governed by ACLs, which easydrop does not
    set – so on a Windows machine the vault is readable by other local users.
    Deploying *to* Windows is not supported at all (targets are Debian/Ubuntu);
    this only affects easydrop running as an MCP server on a Windows host. Treat
    that machine as one you would not share, or use `server.ssh_key` and skip
    `manage_server` entirely.
*   **Targets are Debian and Ubuntu**, on Docker Engine – no rootless Podman, no
    other container runtimes. A snap-confined Docker daemon is rejected with an
    explicit error (see the note in [Development](#development)).

---

## Project Documentation Structure

The core codebase documentation is decoupled by operational boundaries:
*   [CHANGELOG.md](CHANGELOG.md) – Release notes.
*   [PRD.md](PRD.md) – Product scope, operational logic, and functional requirements.
*   [ARCHITECTURE.md](ARCHITECTURE.md) – Component layouts, state management specifications, and internal Go package map.
*   [docs/implementation.md](docs/implementation.md) – Technical roadmap and exact build execution steps for the AI agent.
*   [docs/cli-spec.md](docs/cli-spec.md) – Full interface specification for terminal commands and execution flags.
*   [docs/mcp-spec.md](docs/mcp-spec.md) – JSON-RPC tool schemas and target context resources for LLMs.
*   [examples/README.md](examples/README.md) – Annotated `easydrop.toml` for every supported scenario, plus the full key reference.

---

## Development

Requires Go >= 1.27 (see `go.mod`; `GOTOOLCHAIN=auto` resolves it).

```bash
make            # list all targets
make build      # compile bin/easydrop (version stamped from git describe)
make verify     # CI gate: gofmt check + go vet + tests
make test-race  # tests under the race detector
make cover      # coverage report -> bin/coverage.out
make release    # cross-compile linux/darwin/windows × amd64/arm64 + SHA256SUMS
make platforms  # list the release platforms, one per line
make build-platform PLATFORM=darwin/arm64    # compile just one triple
make check-platforms    # fail if the CI matrix and the Makefile disagree
make check-version EXPECTED=1.0.0   # fail if the source version and CHANGELOG disagree
make smoke      # end-to-end deploy of a throwaway app on the local docker daemon
```

Override the stamped version explicitly for releases: `make build VERSION=1.2.3`.

**Continuous integration.** [`.github/workflows/ci.yml`](.github/workflows/ci.yml)
runs on every push and pull request:

| Job | What it proves |
| --- | --- |
| `verify` | gofmt, `go vet`, the test suite, and the race detector – the authoritative gate |
| `smoke` | a real end-to-end deploy on a runner with Docker |
| `build` | all six triples (`linux`/`darwin`/`windows` × `amd64`/`arm64`) compile, one job each so a break names the platform |
| `test-native` | the suite runs natively on Linux, macOS and Windows – a cross-compiled binary is never *run* by the build job |
| `release` | on a `v*` tag: every platform plus `SHA256SUMS`, attached as one artifact |

Two drift guards run before anything is built, because both failure modes are
silent otherwise:

- `make check-platforms` runs in `verify`. The platform list lives in the
  `Makefile` (`PLATFORMS`) and the workflow repeats it; if the two ever diverge,
  the build stops.
- `make check-version EXPECTED=<tag>` runs first in `release`. It refuses to
  publish a tag whose number disagrees with `internal/version.Version` or with the
  `CHANGELOG.md` heading — a release whose binaries report a different version
  than the tag they were published under is worse than no release, and nothing
  downstream would notice.

Publishing to a release page is deliberately left to a human.

> **Snap-docker hosts:** a snap-confined daemon has its own mount namespace – it cannot see host `/tmp`, cannot read hidden (`dot-`) files in `$HOME`, and resolves `$HOME` to a snap path inside its own commands. EasyDrop detects this, fails fast with an actionable message, and automatically keeps Compose/Swarm state in a non-hidden `~/easydrop` directory. To build, set `EASYDROP_STAGING_BASE` to a **non-hidden** path under `$HOME` (e.g. `~/easydrop-staging`); `make smoke` does this automatically. Hosts bootstrapped by easydrop use apt Docker and never hit these limits. Full reference: [`docs/implementation.md §10`](docs/implementation.md).

## License

Distributed under the MIT License. See [LICENSE](LICENSE.md) for more information.
