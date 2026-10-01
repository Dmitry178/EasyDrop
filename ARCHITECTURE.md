# Architecture Specification – EasyDrop

## 1. Architectural Pattern (One Core, Two Interfaces)
The system is strictly decoupled into an isolated core business logic layer (`internal/core`) and two independent user-facing interfaces (the terminal CLI and the JSON-RPC MCP Server). The core remains entirely agnostic of the interface initiating the execution request and returns strongly-typed Go structural data.

```mermaid
graph TD
    CLI[cmd/easydrop CLI commands] -->|Method Invocations| Core[internal/core]
    MCP[cmd/easydrop mcp-server] -->|Method Invocations| Core

    Core --> Config[internal/config]
    Core --> Drivers[internal/core/drivers]
    Core --> State[internal/state]
```

Single binary: `cmd/easydrop/main.go` exposes both CLI (cobra) and
MCP server (`easydrop mcp-server` switches the binary into JSON-RPC stdio mode).

## 2. Repository Structure
```text
easydrop/
├── cmd/
│   └── easydrop/             # Single binary entry point (CLI + `mcp-server` subcommand)
├── internal/
│   ├── config/               # Validation and parsing engines for easydrop.toml
│   ├── state/                # Deployment state manager (Source of Truth)
│   ├── models/               # Domain Models and shared data structures
│   └── core/                 # Core business logic layer
│       ├── bootstrapper/     # Environment verification and Docker/Compose provisioning
│       ├── builder/          # Image compilation pipelines (Local/Remote blueprints)
│       ├── drivers/          # Orchestration drivers (Single, Compose, Swarm structures)
│       └── infra/            # Ingress management (Nginx & Certbot operations)
├── templates/                # Embedded blueprints: nginx.conf.tmpl + templates.go (package templates, go:embed – embed forbids `..`, so infra imports it as a package)
└── pkg/                      # Generic utility packages and shared toolsets
```

## 3. State Management & Idempotency
Because EasyDrop functions as a stateless CLI utility without a persistent daemon running on the user's local machine, tracking and anchoring active infrastructure state occurs directly on the targeted remote host.

1. **Source of Truth:** A dedicated runtime file is maintained natively on the target host filesystem under the path `~/.easydrop/state/[app_name].json` to lock operational states.
2. **Fault Tolerance & Rollbacks:** When executing rolling updates via the Single driver, the engine spins up the incoming container on an ephemeral transient port first, kicks off network validation loops (Health Checks), and safely tears down the legacy active container *only* after ensuring successful staging handshakes and updating active Nginx upstream configurations.

## 4. Drivers Interface Specification
Every custom container orchestration module must conform strictly to a unified structural contract written in Go (locked – `Deploy` takes only `*models.Application`; `Logs` covers both MCP slice via channel close and CLI `tail -f` streaming):

```go
package drivers

import (
	"context"
	"easydrop/internal/models"
)

type DeploymentDriver interface {
	Deploy(ctx context.Context, app *models.Application) error
	Status(ctx context.Context, appName string) (*models.AppStatus, error)
	Logs(ctx context.Context, appName string, lines int, follow bool) (<-chan string, error)
	Rollback(ctx context.Context, app *models.Application) error
	Teardown(ctx context.Context, appName string) error
}

// CommandExecutor abstracts local vs remote (SSH) host operations.
// Close() is mandatory to avoid SSH/SFTP connection leaks.
// Nginx configs are staged via UploadFile to /tmp/easydrop/ and moved
// atomically with `sudo mv` – never written directly to /etc/nginx.
// Certbot without email uses --register-unsafely-without-email.
type CommandExecutor interface {
	ExecCommand(ctx context.Context, cmd string) (stdout, stderr string, exitCode int, err error)
	UploadFile(ctx context.Context, srcPath, destPath string) error
	Close() error
}
```

`models.Application` is the compiled runtime context (`{ Config *Config }`) passed through the core; `models.AppStatus{ Status, Uptime, SSLStatus }` is the `Status` return type. Full field map (`HealthCheckPath` default `/`, `Build.Registry/Image/NoCache`, `Driver.ComposeFile` default `docker-compose.yml`, `Nginx.Email` optional) lives in `internal/models/types.go` and `docs/implementation.md §1`.

## 5. Locked Tech Decisions

| # | Decision | Choice | Why |
|---|----------|--------|-----|
| 1 | Distribution | Single binary `cmd/easydrop/main.go`; MCP mode = `easydrop mcp-server` (stdio) | Two binaries complicate cross-compilation and distribution (NFR-03) |
| 2 | TOML | `github.com/pelletier/go-toml/v2` | Struct tagging, strict type validation at parse time, JSON-schema generation for the MCP `easydrop://docs/schema` resource |
| 3 | CLI framework | `cobra`, no `viper` | Industry standard for CLI; `viper` is overkill – all config lives in one `easydrop.toml`, cobra flags suffice |
| 4 | MCP SDK | Official `modelcontextprotocol/go-sdk` | Most stable, maintained, lightweight Go SDK; maps core methods to JSON-RPC tools for LLMs |
| 5 | Config scope | Extended `Config` (registry / healthcheck / compose fields) | Avoids uncovered requirements: FR-04 local build needs `Registry/Image/NoCache`, FR-12 needs `HealthCheckPath`, FR-06 needs `ComposeFile` |
| 6 | Toolchain | Go ≥ 1.27 (`go 1.27` in `go.mod`; system SDK `~/go/go1.27.1` first on `PATH`) | Latest stable at setup; `x/crypto` needs ≥ 1.26 |
| 7 | Blue-Green opt-in | Direct in-place redeploy by default; Blue-Green via `driver.blue_green` / `deploy --blue-green` | Zero-downtime must not surprise: extra port, backup container, longer pipeline – explicit choice |
| 7b | Build strategies | `remote` (default): ship workspace, build on target, no registry. `local`: build on the EasyDrop machine, push to `build.registry`, pull on target (single driver only) | Registry-free stays the default; CI runners and shared registries get a first-class path |
| 8 | Rollback | `Rollback(ctx, app)` on the driver; backup = stopped `[app]-active-previous` kept by Blue-Green deploys, consumed on rollback, purged by teardown | Image-tag juggling needs builder changes; a stopped backup container is driver-local, exact and fast |

## 6. Open Decisions (require a product call – details in `docs/implementation.md §9`)

| # | Question | Why it matters | Proposal |
|---|----------|----------------|----------|
| OD-01 | Split container-internal vs host-published ports? | Single `app.port` forces the image to listen on the host port; fixed-port images (nginx:80) can't move | RESOLVED (M10): `app.port` = in-container port, optional `app.host_port` (default = `port`) = published host port; Blue-Green pair = `{host_port, host_port+1}` |
| OD-02 | How to handle snap-confined dockerd? | Private mount namespace: no host `/tmp`, no dot-files in `$HOME`, synthetic `$HOME` in daemon-spawned commands → builds and compose/swarm state fail cryptically | RESOLVED (M9/M12): `IsSnapConfinedDaemon` fail-fast + `EASYDROP_STAGING_BASE` (non-hidden) hint, non-hidden `~/easydrop` state dir, `resolveHome` – see `docs/implementation.md §10` |
```
