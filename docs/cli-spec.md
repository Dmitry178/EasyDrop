# CLI Interface Specification

Framework: `github.com/spf13/cobra`, no `viper` (single `easydrop.toml`, cobra flags suffice).
Single binary `cmd/easydrop/main.go` (see ARCHITECTURE.md §5).

## 1. Base Commands & Syntax

### 1.0. Global flags
- `-c, --config string`: path to `easydrop.toml` (defaults to `./easydrop.toml`). Applies to `deploy`, `status`, `logs`, `rollback`, `teardown` (and `mcp-server` ignores it – MCP receives `config_path` per call).

### 1.0.1. Credentials in the config (M13)
- **Secret files:** `easydrop` reads the optional `.env` and `.easydrop.env` located in the **same directory as the config file** (`.easydrop.env` wins if both exist). A missing file is not an error. Both are git-ignored and are excluded from the archive shipped to the target host; a project that needs `.env` in the bundle re-includes it with `!.env` in `.dockerignore`.
- **Precedence:** real process environment > `.easydrop.env` > `.env` (CI can therefore inject a value with no file at all).
- **Syntax:** `${VAR}` (required – unset or empty is an error naming the field, before any SSH connection), `${VAR:-default}`, `$$` for a literal dollar sign. Applies to the string fields only: `app.name`, `app.health_check_path`, `server.host`, `server.user`, `server.ssh_key`, `server.password`, `build.registry`, `build.image`, `driver.compose_file`, `nginx.domain`, `nginx.email`. Numbers and booleans are never interpolated.
- **Never** echo a resolved value in errors, output or logs (NFR-02).

### 1.1. `easydrop init`
Initializes a new project workspace in the current working directory.
- **Behavior:** Scans the active directory for existing deployment and application assets (such as a `Dockerfile`, `docker-compose.yml`, `package.json`, or `go.mod`). Generates an optimized, pre-filled `easydrop.toml` template.
- **Flags:**
  - `--force`: Overwrite an already existing `easydrop.toml` file without prompting for confirmation.

### 1.2. `easydrop deploy`
Triggers the comprehensive application build and deployment pipeline onto the target environment.
- **Behavior:** Parses the active `easydrop.toml`, inspects host server configurations and runtime engines, produces the image, updates ingress networking routes, and mounts the active containers. Image production follows `build.strategy`: `remote` (default) ships the workspace and builds on the host; `local` builds here, pushes to `build.registry` and pulls on the host – that strategy requires `build.registry` and the `single` driver.
- **Flags:**
  - `-c, --config string`: Explicit path targeting the custom configuration blueprint file (defaults to `./easydrop.toml`).
  - `--no-cache`: Bypass the Docker build cache for this run (overrides `build.no_cache` from `easydrop.toml` to `true`). Applies to both strategies: the on-host build (`build.strategy = "remote"`) and the local build + push (`"local"`).
  - `--blue-green`: Enable zero-downtime Blue-Green deployment for this run (overrides `driver.blue_green` from `easydrop.toml` to `true`). Default (flag absent, config false): direct in-place redeploy with brief downtime.
  - `--skip-bootstrap`: Skip host provisioning (Bootstrapper). Use when docker is already installed and privileges are arranged (dev boxes without passwordless sudo, CI runners, managed hosts).

### 1.3. `easydrop status`
Queries and displays the active system metrics and health landscapes of the deployed application stack.
- **Behavior:** Establishes a transient connection to the host machine to extract current runtime flags, operational container lifecycle stages, resource allocations, active uptimes, and TLS/SSL expiration matrices. App name is taken from `app.name` in the resolved `easydrop.toml`.

### 1.4. `easydrop logs [app_name]`
Streams operational container output channels directly into the terminal interface.
- **Behavior:** If `[app_name]` is omitted, `app.name` from the resolved `easydrop.toml` is used. Backed by `Logs(ctx, appName, lines, follow)` – `lines` = `--tail`, `follow` = `--follow`.
- **Flags:**
  - `-f, --follow`: Stream live stdout/stderr data logs from the remote host environment in real time (equivalent to `tail -f`).
  - `-n, --tail int`: Number of historical trace log lines to render upon initial attachment (defaults to 100).

### 1.5. `easydrop rollback [app_name]`
Restores the stopped backup kept by the last Blue-Green deploy.
- **Behavior:** If `[app_name]` is omitted, `app.name` from the resolved `easydrop.toml` is used. Backed by `Rollback(ctx, app)`: removes the failed active, renames `[app]-active-previous` back, starts it, repoints ingress, probes health. Fails fast with "no rollback backup" when no backup exists (direct-mode deploys, fresh hosts) – without touching anything.

### 1.6. `easydrop teardown [app_name]`
Removes the deployment from the target host.
- **Behavior:** If `[app_name]` is omitted, `app.name` from the resolved `easydrop.toml` is used. Backed by `Teardown` on the configured driver: removes containers (including the stopped Blue-Green backup `[app]-active-previous` and any leftover `-green`), and for Compose/Swarm also the stack (`compose down` / `docker stack rm`) plus its state directory. **Named volumes are never deleted** – data outlives the deploy. Idempotent: tearing down an app that is not deployed succeeds with a "not present, skipping" note.
- **Flags:** `-c, --config string` (defaults to `./easydrop.toml`).

### 1.7. `easydrop mcp-server`
Starts the MCP server in stdio JSON-RPC mode (same binary, see `docs/mcp-spec.md`).
- **Behavior:** Switches `cmd/easydrop` into server mode speaking MCP over stdio using the official `modelcontextprotocol/go-sdk`. AI clients configure `command: easydrop, args: ["mcp-server"]` (transport `stdio`).
- **Flags:** none.
