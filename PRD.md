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

### 2.2. Artifact Build Strategies
- **FR-03 (Remote Build):** The system must support shipping raw source code to the target host to build the Docker image directly on the server, removing external registry dependencies.
- **FR-04 (Local Build):** The system must support building images locally, pushing them to a user-defined public or private Docker Registry, and pulling them on the target server.

### 2.3. Orchestration Modes (Drivers)
- **FR-05 (Solo):** Provisioning and lifecycle management of isolated, single-container deployments.
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
- **NFR-02 (Security):** Local storage of server credentials must be isolated and secured against unauthorized access. Private SSH keys and passwords must never be exposed in system logs.
- **NFR-03 (Portability):** The tool must ship as a single compiled executable supporting Linux, macOS, and Windows across both amd64 and arm64 architectures. The same binary serves CLI and MCP: `easydrop mcp-server` switches it into MCP stdio mode (no separate `easydrop-mcp` binary).

## 4. Locked Tech Decisions (binding; details in ARCHITECTURE.md §5)

- **D-01 Single binary:** `cmd/easydrop/main.go` (CLI via `cobra` without `viper`, MCP via official `modelcontextprotocol/go-sdk`).
- **D-02 Config parsing:** `github.com/pelletier/go-toml/v2`.
- **D-03 Extended `easydrop.toml` schema:** FR-04 local builds use `build.registry` / `build.image` / `build.no_cache` (`deploy --no-cache` / `deploy_app.no_cache` override); FR-12 health checks use `app.health_check_path` (default `"/"`); FR-06 compose uses `driver.compose_file` (default `"docker-compose.yml"`).
- **D-04 Core contracts:** `Deploy(ctx, app *Application)`; `Logs(ctx, appName, lines, follow) (<-chan string, error)` (covers MCP slice + CLI `tail -f`); `CommandExecutor` always has `Close() error`.
- **D-05 Privileged files & TLS:** Nginx configs are staged to `/tmp/easydrop/` via `UploadFile` then moved with `sudo mv` (never written to `/etc/nginx` directly); empty Certbot email means `--register-unsafely-without-email`; `localhost`/`127.0.0.1` skips Certbot.
- **D-06 Server store:** `manage_server` MVP persists to a local `0600`-permission file; encrypted vault is deferred.
