# Product Requirements Document (PRD) – EasyDrop

## 1. Introduction & Project Goals

### 1.1. Purpose
EasyDrop is a lightweight deployment automation tool for shipping Docker applications to any Linux environment (localhost, VPS, bare-metal, or Raspberry Pi). The project addresses the excessive complexity of enterprise orchestrators (Kubernetes, Nomad) for small-to-medium use cases, providing a streamlined alternative compiled into a single binary.

### 1.2. Target Audience
- **Indie Hackers & Solo Developers:** Require rapid MVP deployment loops without recurring PaaS subscription fees, bringing a PaaS-like workflow to their own infrastructure.
- **AI Assistant Users (Cursor, Claude Code):** Leverage automation via the Model Context Protocol (MCP) to manage application infrastructure autonomously using natural language.

### 1.3. Value Proposition
- **Zero-Config Reverse Proxy:** Automated external traffic routing and transparent SSL/TLS certificate management (HTTPS out of the box).
- **Registry-Free Build Autonomy:** Capability to deploy apps without an external Docker Registry by compiling images directly on the target host.
- **AI-Native Interface:** Architecture designed from the ground up to be parsed and driven by LLM agents.

## 2. Functional Requirements

### 2.1. Configuration Management
- **FR-01:** The system must initialize and parse a declarative project configuration file using the TOML format.
- **FR-02:** The tool must automatically scan the current working directory (detecting language stacks, ports, and Dockerfiles) to generate a pre-populated boilerplate configuration.
- **FR-02A (Secret resolution):** The system must support `${VAR}` references in the string fields of the configuration, resolved at parse time from the process environment and from optional `.easydrop.env` / `.env` files located next to the config (precedence: process environment > `.easydrop.env` > `.env`). An unset required reference is a hard error that names the field but never the value. Those files are git-ignored and must be excluded from the archive shipped to the target host.

### 2.2. Artifact Build Strategies
- **FR-03 (Remote Build):** The system must support shipping raw source code to the target host to build the Docker image directly on the server, removing external registry dependencies.
- **FR-04 (Local Build):** The system must support building images locally, pushing them to a user-defined public or private Docker Registry, and pulling them on the target server.

### 2.3. Orchestration Modes (Drivers)
- **FR-05 (Single):** Provisioning and lifecycle management of isolated, single-container deployments.
- **FR-06 (Compose):** Deploying multi-container applications using standard declarative stack specification files.
- **FR-07 (Swarm):** Supporting clustered deployment topologies using Docker Swarm for high-availability setups.

### 2.4. Infrastructure & Networking
- **FR-08:** Automatic generation of a reverse proxy configuration to route incoming HTTP/HTTPS traffic to the application containers.
- **FR-09:** Automatic provisioning, installation, and renewals of Let's Encrypt SSL/TLS certificates for targeted domain names.

### 2.5. Environment Bootstrapping
- **FR-10:** Automated health check and preparation of a clean target Linux server: installing Docker Engine, Docker Compose, and setting up appropriate user group privileges without manual VPS configuration.

### 2.6. Monitoring & Diagnostics
- **FR-11:** Querying active service health statuses and streaming remote stdout/stderr container logs in real time.
- **FR-12 (Health Checking):** Validating networking and application availability before concluding the deployment pipeline successfully.

## 3. Non-Functional Requirements

- **NFR-01 (Idempotency):** Executing a deployment workflow multiple times without changes must not trigger resource duplication or introduce application downtime.
- **NFR-02 (Security):** Local storage of server credentials must be isolated and secured against unauthorized access. Private SSH keys and passwords must never be exposed in system logs, in error messages, or in the workspace archive transferred to the target host.
- **NFR-03 (Portability):** The tool must ship as a single compiled executable supporting Linux, macOS, and Windows across both amd64 and arm64 architectures. The same binary serves CLI and MCP: `easydrop mcp-server` switches it into MCP stdio mode (no separate `easydrop-mcp` binary).

## 4. Locked Tech Decisions (binding; details in ARCHITECTURE.md §5)

- **D-01 Single binary:** `cmd/easydrop/main.go` (CLI via `cobra` without `viper`, MCP via official `modelcontextprotocol/go-sdk`).
- **D-02 Config parsing:** `github.com/pelletier/go-toml/v2`.
- **D-03 Extended `easydrop.toml` schema:** FR-04 local builds use `build.registry` / `build.image` / `build.no_cache` (`deploy --no-cache` / `deploy_app.no_cache` override); FR-12 health checks use `app.health_check_path` (default `"/"`); FR-06 compose uses `driver.compose_file` (default `"docker-compose.yml"`).
- **D-04 Core contracts:** `Deploy(ctx, app *Application)`; `Logs(ctx, appName, lines, follow) (<-chan string, error)` (covers MCP slice + CLI `tail -f`); `CommandExecutor` always has `Close() error`.
- **D-05 Privileged files & TLS:** Nginx configs are staged to `/tmp/easydrop/` via `UploadFile` then moved with `sudo mv` (never written to `/etc/nginx` directly); empty Certbot email means `--register-unsafely-without-email`; `localhost`/`127.0.0.1` skips Certbot.
- **D-06 Server store:** `manage_server` persists to an encrypted vault (`~/.easydrop/servers.vault`, AES-256-GCM + scrypt, `0600`); the key comes only from `EASYDROP_VAULT_PASSWORD` and there is no interactive prompt – the tool fails closed. Legacy plaintext `servers.toml` migrates once.
- **D-07 Blue-Green opt-in:** default deploy is direct in-place (brief downtime); zero-downtime Blue-Green swaps require `driver.blue_green = true` or `deploy --blue-green` (`deploy_app.blue_green`).
- **D-08 Rollback:** Blue-Green deploys keep the retired container stopped as `[app]-active-previous`; `easydrop rollback` / `rollback_app` restores, starts, repoints ingress and probes it, consuming the backup. No backup (direct deploys) → fail-fast error, zero mutations.
- **D-09 Drivers:** `driver.type` selects `single` (single container, direct or opt-in Blue-Green), `compose` (multi-container stack) or `swarm` (single-node swarm MVP). Blue-Green is single-only; compose/swarm roll back via the previous compose/stack file. Interface layer holds no hardcoded driver.
- **D-11 Build strategies:** `build.strategy = "remote"` (default) ships the workspace and builds on the target – no registry needed. `"local"` builds on the EasyDrop machine, pushes to `build.registry` (`build.image` optional) and pulls on the target; single driver only.
- **D-10 Snap-confined daemons:** detected and handled – fail-fast with `EASYDROP_STAGING_BASE` hint for `/tmp` staging, plus a non-hidden `$HOME/easydrop` state dir (snapd blocks hidden files in `$HOME`).

## 5. Open Product Decisions (details in ARCHITECTURE.md §6 / `docs/implementation.md` §9)

- **OD-00 Local build (FR-04):** RESOLVED (M11). `build.strategy = "local"` builds the image on the machine running EasyDrop, pushes it to `build.registry` and pulls it on the target host – nothing but the image crosses the wire. Supported by the `single` driver; compose/swarm build on the host and reject it explicitly.
- **OD-01 Port split:** RESOLVED (M10). `app.port` is the in-container listen port; optional `app.host_port` (default = `app.port`) is the published host port, and the Blue-Green pair is `{host_port, host_port+1}`. Fixed-port images (`nginx:80`) now publish on any free host port, and configs without `host_port` behave exactly as before.
- **OD-02 Snap-docker hosts:** RESOLVED (M9, expanded in M12). A snap-confined `dockerd` has its own mount namespace with three limits: no host `/tmp` (breaks workspace staging/builds), no hidden `dot-` files in `$HOME` (breaks compose/swarm state and dot-dir build contexts), and a synthetic `$HOME` inside daemon-spawned commands (breaks `'$HOME/...'` in composed commands). EasyDrop probes the daemon once, fails fast with an actionable `EASYDROP_STAGING_BASE` hint, stores compose/swarm state in a non-hidden `~/easydrop` on snap hosts, and resolves `$HOME` before use. Full reference: `docs/implementation.md §10`. Hosts provisioned by Bootstrapper (apt-docker) are unaffected.
- **OD-03 Credentials in version control:** RESOLVED (M13). A literal `server.password` lives in git; the encrypted vault does not help because `manage_server` is MCP-only and nothing in the deploy path reads it. `${VAR}` interpolation plus optional `.easydrop.env`/`.env` (FR-02A) keeps the config committable, and the secret files are excluded from the shipped archive. `ssh-agent` remains the zero-config path for key auth.
