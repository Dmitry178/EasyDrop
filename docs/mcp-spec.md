# Model Context Protocol (MCP) Server Specification

Transport: stdio via the same binary – `easydrop mcp-server` (see `docs/cli-spec.md §1.6`).
SDK: official `modelcontextprotocol/go-sdk` (locked, see ARCHITECTURE.md §5).
Single binary, no separate `easydrop-mcp` executable.

## 1. JSON-RPC Tools Schema

AI assistants invoke these structured primitives to safely manage infrastructure configurations. The underlying MCP server pipes these method parameters directly into targeted execution routines within the `internal/core` logic layer.

### 1.1. `init_project`
- **Description:** Scans the active local application layout to dynamically generate an optimized, pre-filled boilerplate deployment configuration file.
- **Arguments:**
  - `force` (boolean, optional): Overwrite an already existing `easydrop.toml` without prompting. Defaults to `false`. (Mirrors CLI `init --force`.)

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
- **Description:** Reads current remote stdout/stderr application output streams for immediate debugging or error root-cause diagnostics. Backed by `Logs(ctx, appName, lines, follow)` – MCP collects the channel to a list (streaming `follow` is supported; the server closes the stream on client cancel).
- **Arguments:**
  - `app_name` (string, required): Targeted application name identifier.
  - `tail` (integer, optional): Total lines of log context history to retrieve. Defaults to 50.
  - `follow` (boolean, optional): Keep streaming new lines (mirrors CLI `logs --follow`). Defaults to `false`.

### 1.5. `rollback_app`
- **Description:** Restores the stopped backup kept by the last Blue-Green deploy (mirrors CLI `rollback`). Backed by `Rollback(ctx, app)`. Fails fast with "no rollback backup" when no backup exists – without touching anything.
- **Arguments:**
  - `app_name` (string, required): Targeted application name identifier.
  - `config_path` (string, optional): Deployment blueprint for ports/healthcheck/ingress settings. Defaults to `./easydrop.toml`.

### 1.6. `manage_server`
- **Description:** Mutates host records in EasyDrop's local server store to safely bind, evaluate, or deprecate environment access configurations.
- **Persistence (locked):** MVP = local file store with `0600` permissions (e.g. `~/.easydrop/servers.toml`); encrypted vault is deferred to Milestone 9. Never log secrets (NFR-02).
- **Arguments:**
  - `action` (string, required): Operational mutating intent flag. Permitted settings strictly match: `"add"` or `"remove"`.
  - `config` (object, required): Explicit server properties containing credential attributes (`host`, `user`, and optionally matching `ssh_key` or `password`).

## 2. Exported Resources Schema
The protocol maps contextual state metrics allowing connected LLM instances to autonomously query baseline environment criteria:
- `easydrop://docs/schema`: Active structural JSON Schema validation blueprint enforcing correct structural rules on `easydrop.toml` parameters. Generated from the `internal/models` structs (parsed with go-toml/v2).
- `easydrop://docs/troubleshooting`: Systemic operational runbook compiling standard infrastructure mitigation paths (such as port conflicts, failing container network probes, or missing proxy headers) for automated self-healing execution loops.

## 3. Tool → Core mapping
| Tool | Core entrypoint |
|------|-----------------|
| `init_project` | config scaffolding (Milestone 7, FR-02) |
| `deploy_app` | `Bootstrapper.Bootstrap` → builder `Build(ctx, app)` → `SingleDriver.Deploy(ctx, app)` → Nginx/Certbot |
| `get_status` | `SingleDriver.Status(ctx, appName)` |
| `get_logs` | `SingleDriver.Logs(ctx, appName, lines, follow)` |
| `rollback_app` | `SingleDriver.Rollback(ctx, app)` |
| `manage_server` | local server store (`0600` file; encrypted vault deferred) |
