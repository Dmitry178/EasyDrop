# Technical Implementation Steps & Roadmap

This document outlines the chronological execution order, core data contracts, and specific acceptance criteria for each system module. Steps must be developed sequentially. Every micro-task includes a dedicated checkbox to explicitly track progress.

---

## §0. Sequence of Execution

Development is decoupled into isolated milestones. Proceeding to a subsequent milestone is strictly conditional upon 100% compilation and passing unit tests of the current phase.

- [x] **Milestone 1:** Configuration Parsing Architecture (`internal/config` & `internal/models`) – DONE. Stack: `github.com/pelletier/go-toml/v2`.
- [x] **Milestone 2:** Host Environment Abstraction Layer – DONE (`CommandExecutor` with `Close()`, `SSHExecutor`, `LocalExecutor`, factory; `internal/core/*_test.go` green).
- [x] **Milestone 3:** Target Environment Autonomic Bootstrapper – DONE (`Bootstrapper` + runtime tag/arch resolution; `bootstrap_test.go` green).
- [x] **Milestone 4:** Automated Source Compiling Engine – DONE (`RemoteBuilder` + tar.gz archiver with .dockerignore; `builder/*_test.go` green; `LocalBuilder` deferred to M9).
- [x] **Milestone 5:** Single Container Orchestration Driver – DONE (`SingleDriver` direct default + opt-in Blue-Green on the {Port, Port+1} pair, backup + `Rollback`, Status/Logs/Teardown; `drivers/single_test.go` green).
- [x] **Milestone 6:** Ingress Networking Infrastructure Layer – DONE (`NginxManager.Apply/UpdateIngress` + `CertbotManager.EnableSSL`; `infra/*_test.go` green).
- [x] **Milestone 7:** Single-binary CLI – DONE (`cmd/easydrop` + `internal/cli` + `config.Scaffold`; real localhost deploy/status/logs/BG/rollback verified via built binary).
- [x] **Milestone 8:** MCP Server – DONE (`internal/mcp` on official SDK + shared `internal/deploy` core; handler/store/e2e tests + live stdio smoke green).
- [ ] **Milestone 9 (deferred):** Compose/Swarm drivers, encrypted server vault (`manage_server` persistence), snap-docker fail-fast detection (OD-02 B-lite). Open product calls live in §9 (OD-01 ports, OD-02 snap-docker).

> Locked decisions (see ARCHITECTURE.md §5): single binary `cmd/easydrop/main.go`;
> TOML `github.com/pelletier/go-toml/v2`; CLI `cobra` (no `viper`);
> MCP official `modelcontextprotocol/go-sdk`;
> `Logs(ctx, appName, lines, follow) (<-chan string, error)`;
> `CommandExecutor` always has `Close() error`;
> Nginx upload = stage to `/tmp/easydrop/` + `sudo mv`;
> Certbot without email = `--register-unsafely-without-email`.
>
> Toolchain: Go ≥ 1.27 (pinned via `go 1.27` in `go.mod`. System install:
> `~/go/go1.27.1`, first on `PATH` via `~/.bashrc`; `GOTOOLCHAIN=auto` as fallback).

---

## §1. Configuration Blueprint & Domain Models

### 1.1. Data Struct Modeling (`internal/models/types.go`)
- [x] Define `AppConfig` for the `[app]` block: `Name` (string), `Port` (int), `HealthCheckPath` (string, default `"/"`).
- [x] Define `ServerConfig` for the `[server]` block: `Host` (string), `User` (string), `SSHKey` (string), `Password` (string), `Port` (int).
- [x] Define `BuildConfig` for the `[build]` block: `Strategy` (`"remote"`|`"local"`), `Registry` (string, for FR-04 local build), `Image` (string), `NoCache` (bool, settable via `deploy --no-cache` flag override).
- [x] Define `DriverConfig` for the `[driver]` block: `Type` (`"single"`|`"compose"`|`"swarm"`), `ComposeFile` (string, default `"docker-compose.yml"`, only for `compose`), `BlueGreen` (bool, default `false` – Blue-Green is opt-in via config or `deploy --blue-green` override).
- [x] Define `NginxConfig` for the `[nginx]` block: `Domain` (string), `SSL` (bool), `Email` (string, optional – empty means `--register-unsafely-without-email`).
- [x] Consolidate all configuration maps into a single root structural type named `Config`.
- [x] Define runtime types: `Application{ Config *Config }` (compiled context passed to drivers) and `AppStatus{ Status, Uptime, SSLStatus }` (returned by `Status`).

### 1.2. Configuration Parser Engine (`internal/config/parser.go`)
- [x] Import `github.com/pelletier/go-toml/v2` (locked; NOT `BurntSushi/toml` – go-toml/v2 gives better struct tagging, strict type validation, and JSON-schema generation for the MCP `easydrop://docs/schema` resource).
- [x] Implement signature: `ParseConfig(path string) (*models.Config, error)`.
- [x] Enforce field validations: return explicit errors if `app.name` or `server.host` are omitted; `build.strategy` must be `"remote"`|`"local"`; `driver.type` must be `"single"`|`"compose"`|`"swarm"`.
- [x] Implement fallback defaults for optional properties:
  - `server.port` = `22`
  - `server.ssh_key` = `~/.ssh/id_rsa` (ensure tilde `~` expansion to absolute system paths).
  - `build.strategy` = `"remote"`
  - `driver.type` = `"single"`
  - `driver.compose_file` = `"docker-compose.yml"` (only when `driver.type == "compose"`)
  - `app.health_check_path` = `"/"`

### 1.3. Parsing Test Suite (`internal/config/parser_test.go`)
- [x] Test Case: Parsing a fully populated valid TOML template.
- [x] Test Case: Asserting initialization failure when critical parameters are missing.
- [x] Test Case: Fallback mapping verification for missing optional attributes.

---

## §2. Command Execution Abstraction (Executor)

### 2.1. Interface Architecture Blueprint (`internal/core/executor.go`)
- [x] Declare the `CommandExecutor` contract (locked – `Close()` is mandatory to avoid SSH/SFTP leaks):
  ```go
  type CommandExecutor interface {
      ExecCommand(ctx context.Context, cmd string) (stdout, stderr string, exitCode int, err error)
      UploadFile(ctx context.Context, srcPath, destPath string) error
      Close() error
  }
  ```
  Non-zero exit = both `exitCode != 0` AND non-nil `err`, so `docker --version` probes work with `if err != nil`.

### 2.2. Native Environment Client (`internal/core/local_client.go`)
- [x] Write `LocalExecutor` implementation satisfying `CommandExecutor`.
- [x] Implement `ExecCommand`: route inputs through native `os/exec.CommandContext`, safely grabbing `stdout`, `stderr`, and evaluating structural exit codes.
- [x] Implement `UploadFile`: map file streams natively using `os.ReadFile`, `os.WriteFile`, or `io.Copy` targeting local scopes. Assign file operational permissions to `0644`.

### 2.3. Cryptographic Remote Tunnel Client (`internal/core/ssh_client.go`)
- [x] Write `SSHExecutor` implementation satisfying `CommandExecutor` (including `Close()`).
- [x] Import dependencies `golang.org/x/crypto/ssh` and `github.com/pkg/sftp`.
- [x] Wire multi-authentication backing: handle both standard private cryptographic keys (with `ssh-agent` discovery) and pure fallback password strings.
- [x] Implement `ExecCommand`: allocate an active `ssh.Session`, pipe input commands, and parse the buffer output.
- [x] Sanitize and escape all input command strings before routing to prevent shell injection vectors (`EscapeShellArg` helper; callers quote untrusted values like domains).
- [x] Implement `UploadFile`: provision an SFTP connection using `sftp.NewClient`, create files on the remote filesystem, and pipe raw bytes. NOTE: never write directly to privileged paths – stage to `/tmp/easydrop/` and move atomically via `ExecCommand("sudo mv ...")` (see §6.1).
- [x] Lazy dial on first use (`ensureClientLocked`); `known_hosts` preferred, insecure fallback documented; remote paths normalized to forward slashes.

### 2.4. Environment Factory Router (`internal/core/factory.go`)
- [x] Write initialization driver logic:
  ```go
  func NewExecutor(cfg *models.ServerConfig) (CommandExecutor, error)
  ```
- [x] Intercept routing conditions: if `cfg.Host` targets `"localhost"` or `"127.0.0.1"`, bypass socket creation entirely and instantiate a `LocalExecutor`. For all external destinations, provision an `SSHExecutor`.

---

## §3. Environment Autonomic Bootstrapper

### 3.1. Infrastructure Inspection Pipeline (`internal/core/bootstrapper/bootstrap.go`)
- [x] Write `Bootstrapper` logic consuming a generic `CommandExecutor`. Shape (locked):
  ```go
  type Bootstrapper struct {
      exec core.CommandExecutor
      Out  io.Writer // progress log, defaults to os.Stderr; tests swap in a buffer
  }
  func New(exec core.CommandExecutor) *Bootstrapper
  ```
- [x] Expose entry execution contract: `Bootstrap(ctx context.Context) error`. Pipeline order: `checkOS → ensureDocker → ensureCompose → ensureDockerGroup → ensureFirewall`.
- [x] OS Validation: Read `/etc/os-release` via `cat /etc/os-release`. Parse rule: first line with prefix `ID=` (must NOT match `ID_LIKE=`), value lowercased with surrounding quotes trimmed. Halt execution if `ID=ubuntu` or `ID=debian` matches fail. (If executing via `LocalExecutor` on macOS/Windows environments, emit a soft diagnostic alert to `stderr` and proceed).
- [x] Container Runtime Verification: Assert `docker --version`. If an invocation error returns, push automated install routines:
  ```bash
  curl -fsSL https://get.docker.com -o get-docker.sh && sh get-docker.sh
  ```
  Re-probe after install; fail if docker is still missing.
- [x] Orchestration Utility Verification: Assert `docker compose version`. If missing, inspect target platform hardware architecture via `uname -m` (allowlist: `x86_64→x86_64`, `aarch64|arm64→aarch64`, `armv7l→armv7`; anything else errors). Resolve the release tag at runtime from the `/releases/latest` redirect (no hardcoded version):
  ```bash
  curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/docker/compose/releases/latest
  # tag = basename of the returned URL, must match ^v[0-9A-Za-z._-]+$ or abort
  ```
  Fetch the appropriate binary from upstream GitHub Releases (`github.com/docker/compose`), write into `~/.docker/cli-plugins/docker-compose`, and flag execution rights to `0755` – as one chain:
  ```bash
  mkdir -p ~/.docker/cli-plugins && curl -fsSL -o ~/.docker/cli-plugins/docker-compose https://github.com/docker/compose/releases/download/[tag]/docker-compose-linux-[arch] && chmod 0755 ~/.docker/cli-plugins/docker-compose
  ```
  Re-probe `docker compose version` after install; fail if still missing.
- [x] Security Privileges Step: Append user scopes to runtime groups: `sudo usermod -aG docker $USER`.
- [x] Firewall Provisioning: Detect systemic presence of `ufw`. If active, ensure traffic rule alignment:
  ```bash
  sudo ufw allow OpenSSH
  sudo ufw allow 'Nginx Full'
  sudo ufw --force enable
  ```
  Hosts without ufw are skipped. Progress logs go to `Bootstrapper.Out` (default `os.Stderr`).

---

## §4. Host Compilation Subsystem (Remote Builder)

### 4.1. File System Compressor Component (`internal/core/builder/archive.go`)
- [x] Construct file directory scanning helpers (`filepath.WalkDir`, modes preserved via `tar.FileInfoHeader`).
- [x] Parse project `.dockerignore` filters if present (absent file = no rules, not an error). Default excludes (always applied, re-includable via `!`): `.git`, `node_modules`, and the target archive bundle `project.tar.gz` itself. Supported glob syntax: `*`, `?`, `**`, leading `/` (anchored) vs basename (any depth), trailing `/` (dir-only), `!` negation with last-match-wins. Excluded dirs prune the walk (`SkipDir`) – re-including a file inside an excluded dir requires `child/*` + `!child/keep` style rules, not a bare dir rule.
- [x] Implement archive consolidation to compress files using `archive/tar` and `compress/gzip`. Regular files and dirs only – symlinks/sockets are skipped. Entry point: `CreateProjectArchive(srcDir string, w io.Writer) error`.

### 4.2. Pipeline Ship & Build Orchestrator (`internal/core/builder/build.go`)
- [x] Create `RemoteBuilder` containing an active `CommandExecutor`. Shape (locked):
  ```go
  type RemoteBuilder struct {
      exec   core.CommandExecutor
      SrcDir string    // workspace to archive; empty = process cwd
      Out    io.Writer // progress log, defaults to os.Stderr
  }
  func NewRemoteBuilder(exec core.CommandExecutor) *RemoteBuilder
  ```
- [x] Declare pipeline signature: `Build(ctx context.Context, app *models.Application) error`. The effective no-cache flag is `app.Config.Build.NoCache` (which `deploy --no-cache` overrides to `true` before calling).
- [x] Validate `app.name` up front against `^[a-z0-9]+(?:[._-][a-z0-9]+)*$` (docker-compatible lowercase) – fails fast with zero host commands instead of a cryptic `docker build -t` rejection.
- [x] Run local workspace compression workflows (into a `os.CreateTemp` bundle, always removed via defer).
- [x] Ship the compiled `project.tar.gz` bundle into isolated workspace storage on the host (`[stagingBase]/builds/[appName]/`, default base `/tmp/easydrop`) via `UploadFile` (parent dirs auto-created by both executors). Escape hatch (locked): env `EASYDROP_STAGING_BASE` overrides the base for hosts where `/tmp` is unsuitable (tiny tmpfs, noexec, snap-confined daemons blind to host `/tmp` – see OD-02 in §9).
- [x] Trigger remote file unpack sequences via `ExecCommand` (paths quoted via `core.EscapeShellArg`):
  ```bash
  tar -xzf /tmp/easydrop/builds/[appName]/project.tar.gz -C /tmp/easydrop/builds/[appName]/
  ```
- [x] Fire runtime container image compiler jobs on the host terminal scope (append `--no-cache` when the effective no-cache flag is set):
  ```bash
  docker build -t easydrop/[appName]:latest /tmp/easydrop/builds/[appName]/
  ```
- [x] Purge temporary environment file footprint `/tmp/easydrop/builds/[appName]/` (`rm -rf`, quoted) upon successful build execution. On build failure the staging dir is intentionally kept for debugging.
- [ ] `LocalBuilder` (FR-04, Milestone 9 or with M4): build locally, push to `BuildConfig.Registry`/`Image`, pull on target. Deferred – M4 stays remote-only.

---

## §5. Single Orchestration Driver Module

### 5.1. Runtime Deployment Implementation (`internal/core/drivers/single.go`)
- [x] Design `SingleDriver` conforming to the unified abstraction contract `DeploymentDriver`. Shape (locked):
  ```go
  type SingleDriver struct {
      exec           core.CommandExecutor
      BlueGreen      bool             // opt-in: config `driver.blue_green`, CLI `--blue-green` forces true
      Ingress        IngressUpdater // nil = skip traffic reroute (local deploys)
      Out            io.Writer      // progress log, defaults to os.Stderr
      ProbeInterval  time.Duration  // default 2s
      MaxProbes      int            // default 10
      FollowInterval time.Duration  // follow-poll interval in Logs, default 2s
  }
  func NewSingleDriver(exec core.CommandExecutor) *SingleDriver
  // IngressUpdater (implemented by M6 infra layer):
  //   UpdateIngress(ctx context.Context, domain string, port int) error
  ```
- [x] Shared validation: `models.ValidateAppName` (`internal/models/validate.go`, `^[a-z0-9]+(?:[._-][a-z0-9]+)*$`) is enforced in builder AND driver Deploy/Status/Logs/Teardown/Rollback.
- [x] `Deploy(ctx, app)` validates, preflights `docker info` (fail fast when the daemon is down), then dispatches by mode. All names/paths quoted via `core.EscapeShellArg`.
- [x] Direct mode (default, `BlueGreen=false`): best-effort `docker rm -f [app]-active`, then `docker run -d --name [app]-active -p [port]:[port] --restart unless-stopped easydrop/[app]:latest`, probe, ingress update (same port). Probe failure leaves the new container running for inspection and returns an error. No backup is kept.
- [x] Blue-Green mode (`BlueGreen=true`) zero-downtime swap sequence:
- [x] Evaluate host landscape state. Check for an active running instance named `[appName]-active` via `docker inspect -f` on `.NetworkSettings.Ports` (missing container = first deploy, not an error).
- [x] Port scheme (locked): only the pair `{app.Port, app.Port+1}` is ever used. Green stages on whichever is free (active's port + alternation: no active → `app.Port`; active on `app.Port` → `app.Port+1`; active on `app.Port+1` → `app.Port`). Generalizes the spec's `app.Port + 1` example and prevents port leaks. NOTE: single `app.Port` doubles as container-internal and host port – splitting them is open decision OD-01 (§9).
- [x] Best-effort `docker rm -f [appName]-green` before staging (leftover from a failed deploy), then assemble and execute the deployment instructions for the isolated staging ("green") instance:
  ```bash
  docker run -d --name [appName]-green -p [temp_port]:[app_internal_port] --restart unless-stopped easydrop/[appName]:latest
  ```
- [x] Init local probing loops: execute networking diagnostics inside the host context hitting `http://localhost:[temp_port][health_check_path]` (path from `app.Config.App.HealthCheckPath`, default `"/"`) every 2 seconds (up to 10 total validation retries) via `curl -fsS -o /dev/null -w '%{http_code}'`, expecting `200`. Any error/non-200 is a miss. `ctx` cancellation aborts the loop.
- [x] If the internal probe captures a valid HTTP `200 OK` handshake, promote the container. Move to ingress traffic rerouting via `Ingress.UpdateIngress(ctx, domain, greenPort)` (skipped when `Ingress == nil` or domain is empty). Ingress failure rolls back: green is removed, production untouched.
- [x] After routing swaps complete, retire the old container into the stopped rollback backup (dropping any older backup):
  ```bash
  docker rm -f [appName]-active-previous
  docker stop [appName]-active && docker rename [appName]-active [appName]-active-previous
  ```
  (skipped on first deploy – nothing to retire).
- [x] Promote staging tags into permanent operational roles:
  ```bash
  docker rename [appName]-green [appName]-active
  ```
- [x] Rollback Strategy: If internal network validation loops yield failures (timeouts), drop staging modifications (`docker rm -f [appName]-green`) without interfering with production traffic, and terminate with an error status. Post-deploy rollback is the `Rollback` method (§5.3), not container surgery by hand.

### 5.2. Status, Logs, Teardown (`internal/core/drivers/single.go`, same file)
- [x] `Status(ctx, appName) (*models.AppStatus, error)`: `docker ps -a --filter 'name=^/[app]-active$' --format "{{.State}}|{{.RunningFor}}"` → running→`Up`, restarting→`Restarting`, anything else/missing→`Down` (uptime from RunningFor). `SSLStatus` stays empty – enriched by the ingress layer (M6).
- [x] `Logs(ctx, appName, lines, follow) (<-chan string, error)`: `docker logs --tail [lines] [app]-active` split into a buffered channel. `follow=false` closes after the snapshot; `follow=true` polls every `FollowInterval`, emitting only unseen lines (dedup window of last 500) until ctx cancellation – polling (not blocking `docker logs -f`) keeps it ctx-aware on both Local and SSH executors.
- [x] `Teardown(ctx, appName) error`: `docker rm -f` active + leftover green + rollback backup (`[app]-active-previous`); missing containers are skipped – teardown is idempotent.

### 5.3. Rollback (`Rollback(ctx, app) error`, same file + interface)
- [x] Contract (locked, part of `DeploymentDriver`): restores the stopped backup kept by the last Blue-Green deploy. Flow: `docker inspect [app]-active-previous` (missing → fail fast with "no rollback backup", zero mutations) → `docker rm -f [app]-active` → `docker rename [app]-active-previous [app]-active` → `docker start [app]-active` → resolve port via inspect (fallback `app.Port`) → ingress update → probe. Consumes the backup: a second rollback reports "no backup" until the next Blue-Green deploy. Probe failure leaves the restored container running (last resort stays up) and returns an error. Direct-mode deploys keep no backup → `Rollback` explains that.

---

## §6. Ingress Network Management (Nginx & Certbot)

### 6.1. Reverse Proxy Provisioner Component (`internal/core/infra/nginx.go`)
- [x] Compose embeddable base config structures inside `templates/nginx.conf.tmpl` backing reactive upstream updates. Embedded via `templates/templates.go` (`package templates`, `//go:embed nginx.conf.tmpl` → `NginxConf string`) – embed patterns forbid `..`, so the template is exposed through a root package instead of a relative path; single binary carries it (NFR-03).
- [x] Bind variable states (`Domain`, `Port`) leveraging `text/template`. Shape (locked):
  ```go
  type NginxManager struct {
      exec core.CommandExecutor
      Out  io.Writer // progress log, defaults to os.Stderr
  }
  func NewNginxManager(exec core.CommandExecutor) *NginxManager
  func (m *NginxManager) Apply(ctx context.Context, domain string, port int) error
  // UpdateIngress(ctx, domain, port) satisfies drivers.IngressUpdater.
  ```
  Validation up front (zero host touch on bad input): domain `^(\*\.)?[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*$`, port 1-65535. Rendered to a local temp file (always removed).
- [x] Deliver compiled reverse proxy settings by STAGING via `UploadFile` to `[stagingBase]/nginx/[domain]` first (locked decision – SFTP cannot write to `/etc/nginx` directly; base from shared `core.StagingBase()`, default `/tmp/easydrop`), then move atomically:
  ```bash
  sudo mv /tmp/easydrop/nginx/[domain] /etc/nginx/sites-available/[domain]
  ```
- [x] Bind active deployment paths through symbolic mapping targets via `ExecCommand`:
  ```bash
  sudo ln -sf /etc/nginx/sites-available/[domain] /etc/nginx/sites-enabled/
  ```
- [x] Run structural routing tests via `sudo nginx -t`. If validations match successfully, apply live settings: `sudo systemctl reload nginx`. Failed `nginx -t` aborts before reload – previous live config keeps serving.

### 6.2. Cryptographic TLS Provisioner Component (internal/core/infra/certbot.go)
- [x] Expose authorization endpoints: `EnableSSL(ctx context.Context, domain, email string) error`. Shape (locked):
  ```go
  type CertbotManager struct {
      exec       core.CommandExecutor
      serverHost string // localhost targets skip provisioning
      Out        io.Writer
  }
  func NewCertbotManager(exec core.CommandExecutor, serverHost string) *CertbotManager
  ```
  Domain validated with the same hostname rule as nginx (zero host touch on bad input).
- [x] Intercept local environment rules: if configuration metrics address `"localhost"` or `"127.0.0.1"`, exit early with a success code (Let's Encrypt does not offer domain validation flows across local network endpoints).
- [x] If `email` is empty, provision with `--register-unsafely-without-email` (locked decision):
  ```bash
  sudo certbot --nginx -d [domain] --non-interactive --agree-tos --register-unsafely-without-email
  ```
  Otherwise:
  ```bash
  sudo certbot --nginx -d [domain] --non-interactive --agree-tos --email [email]
  ```

---

## §7. Single-Binary CLI (`cmd/easydrop`, Milestone 7)

- [x] Single binary entry point `cmd/easydrop/main.go` (locked – no `cmd/cli` + `cmd/mcp-server` split; NFR-03). Command tree lives in `internal/cli/` (`root.go` + one file per command); `main.go` only calls `cli.Execute()`.
- [x] CLI framework `github.com/spf13/cobra` WITHOUT `viper` (locked – single `easydrop.toml`, cobra flags suffice).
- [x] Commands from `docs/cli-spec.md`: `init [--force]` (via `config.Scaffold`+`WriteConfig`), `deploy [-c/--config] [--no-cache] [--blue-green] [--skip-bootstrap]`, `status`, `logs [app_name] [-f/--follow] [-n/--tail]`, `rollback [app_name]`. `mcp-server` lands in Milestone 8.
- [x] `deploy` pipeline (`runDeploy`): parse → overlay `--no-cache`/`--blue-green` (`applyDeployFlags`, never unsets config-true) → gate `driver.type == single` → signal-aware ctx → `NewExecutor` → Bootstrap (skipped with `--skip-bootstrap`) → `Build` with `SrcDir` = config file's directory → `SingleDriver` (`BlueGreen` from config, `Ingress` wired when domain non-empty) → `Deploy` → Certbot when `nginx.ssl && domain != ""` (manager no-ops on localhost).
- [x] `init` scaffolding (`config.Scaffold`, FR-02): EXPOSE port from Dockerfile (default 8080), compose driver on compose files, app name = sanitized dir base, server = localhost + current user. `WriteConfig` refuses overwrite without `--force`; output round-trips through `ParseConfig` (tested).
- [x] Cross-field note (locked): `Bootstrapper.ensureDockerGroup` checks `id -nG` membership first and skips `usermod` when already in the docker group (idempotent, avoids pointless sudo).

## §8. MCP Server (Milestone 8)

- [x] Serve stdio JSON-RPC via `easydrop mcp-server` using the official `modelcontextprotocol/go-sdk` v1.8.0 (locked). `internal/mcp/server.go`: `NewServer()` + `Run(ctx)` over `mcp.StdioTransport{}`; `internal/cli/mcpserver.go` wires the cobra subcommand with signal-aware ctx. Single `internal/version.Version` (`0.1.0`, ldflags-overridable) feeds both `--version` and MCP `serverInfo`.
- [x] One core, two interfaces (locked): the deploy pipeline lives in `internal/deploy/` (`Options`, `Run`, `LoadConfig`, `ResolveAppName`, `ApplyFlags`, `Status`, `Logs` with collection cap, `Rollback`, `Init`); `internal/cli` commands are thin wrappers, MCP handlers call the same functions. No CLI↔MCP imports.
- [x] Tools map 1:1 to `docs/mcp-spec.md`: `init_project`, `deploy_app`, `get_status`, `get_logs`, `rollback_app`, `manage_server`; resources `easydrop://docs/schema` (static JSON Schema mirroring `internal/models`; struct-generated output deferred to M9) and `easydrop://docs/troubleshooting` (static runbook). Typed I/O with `jsonschema` tags; app failures return `isError` tool results (not protocol errors); `follow` log collection capped at 2000 lines.
- [x] `manage_server` persistence: `~/.easydrop/servers.toml` (dir `0700`, file `0600`, upsert by host+user; `EASYDROP_SERVERS_FILE` override for tests); encrypted vault is deferred to Milestone 9. Secrets never echoed in messages or logs (tested).
- [x] Verified: unit tests (store round-trip/perms/validation, handler arg validation, schema registration) + in-process e2e with a real SDK client (`e2e_test.go`: tools/list, init→EXPOSE detect, manage add + secret-leak check, resources, status-without-config isError) + live stdio smoke against the built binary (initialize, tools/list, init_project, manage_server, get_status error path, resources/list+read).

---

## §9. Open Decisions (require a product call – found during M4/M5 smoke tests)

### OD-01: Split container-internal vs host-published ports?
- **Context:** `app.port` currently plays two roles: the container's internal
  listen port AND the managed host-port pair `{Port, Port+1}` (SingleDriver §5).
  An image with a fixed internal port (e.g. `nginx:80`) cannot be published
  on a different host port (e.g. host 80 busy → must use 8080).
- **Affected:** `Config` `[app]` schema, SingleDriver port scheme, Nginx
  upstream port, `docs/cli-spec.md`, MCP schema resource.
- **Options:**
  - **A (status quo):** single `port`. Constraint: the container MUST listen
    on `app.port`; host `{port, port+1}` must be free. Simplest, enough for MVP.
  - **B (recommended when needed):** add optional `app.host_port`
    (default = `port`); managed pair becomes `{host_port, host_port+1}`.
    Backward compatible, small schema addition.
- **Status:** OPEN. MVP proceeds with **A** + documented constraint; switch
  to **B** on the first real deploy that hits it.

### OD-02: How to handle snap-confined Docker daemons?
- **Context (M4 smoke, Ubuntu 24.04 + snap-docker 29.8.0):** a snap-confined
  `dockerd` has a private `/tmp`, so the host staging dir `/tmp/easydrop`
  is invisible to it → `docker build` fails with
  `unable to prepare context: path ... not found`, while `$HOME` paths work.
  Apt-installed `dockerd` (the normal VPS case) sees `/tmp` fine.
- **Impact on the service:** remote builds (FR-03) on snap-docker hosts only.
  Local builds are unaffected. Hosts provisioned by our Bootstrapper get
  apt-docker via `get.docker.com`, so managed hosts never hit this.
- **Already mitigated:** `EASYDROP_STAGING_BASE` env override (§4.2, locked).
- **Options:**
  - **A (status quo):** override + docs. Zero code, covers operators who
    point EasyDrop at a pre-existing snap-docker host.
  - **B-lite (recommended):** fail-fast detection – if `docker info` shows
    snap paths (`/var/snap/docker`) or `Ubuntu Core`, abort the build with
    a clear "snap-docker cannot see host /tmp, set EASYDROP_STAGING_BASE"
    message instead of the cryptic daemon error. Cheap, prevents confusion.
  - **C:** default staging to `$HOME/.easydrop/builds`. Rejected for now –
    weakens the locked `/tmp` default for all hosts to work around one distro quirk.
- **Status:** OPEN. Proposal: schedule **B-lite** with M6/M9; until then the
  override + this note are the documented behavior.
