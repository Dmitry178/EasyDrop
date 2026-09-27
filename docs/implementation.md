# Technical Implementation Steps & Roadmap

This document outlines the chronological execution order, core data contracts, and specific acceptance criteria for each system module. Steps must be developed sequentially. Every micro-task includes a dedicated checkbox to explicitly track progress.

---

## §0. Sequence of Execution

Development is decoupled into isolated milestones. Proceeding to a subsequent milestone is strictly conditional upon 100% compilation and passing unit tests of the current phase.

- [x] **Milestone 1:** Configuration Parsing Architecture (`internal/config` & `internal/models`) – DONE. Stack: `github.com/pelletier/go-toml/v2`.
- [x] **Milestone 2:** Host Environment Abstraction Layer – DONE (`CommandExecutor` with `Close()`, `SSHExecutor`, `LocalExecutor`, factory; `internal/core/*_test.go` green).
- [x] **Milestone 3:** Target Environment Autonomic Bootstrapper – DONE (`Bootstrapper` + runtime tag/arch resolution; `bootstrap_test.go` green).
- [x] **Milestone 4:** Automated Source Compiling Engine – DONE (`RemoteBuilder` + tar.gz archiver with .dockerignore; `builder/*_test.go` green; `LocalBuilder` deferred to M9).
- [ ] **Milestone 5:** Solo Container Orchestration Driver (`SoloDriver` with Blue-Green logic)
- [ ] **Milestone 6:** Ingress Networking Infrastructure Layer (`NginxManager` & `CertbotManager`)
- [ ] **Milestone 7:** Single-binary CLI (`cmd/easydrop`, `cobra` without `viper`, incl. `init`, `deploy`, `status`, `logs`, `mcp-server`)
- [ ] **Milestone 8:** MCP Server (`easydrop mcp-server` stdio, official `modelcontextprotocol/go-sdk`, tools from `docs/mcp-spec.md`)
- [ ] **Milestone 9 (deferred):** Compose/Swarm drivers, encrypted server vault (`manage_server` persistence)

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
- [x] Define `DriverConfig` for the `[driver]` block: `Type` (`"solo"`|`"compose"`|`"swarm"`), `ComposeFile` (string, default `"docker-compose.yml"`, only for `compose`).
- [x] Define `NginxConfig` for the `[nginx]` block: `Domain` (string), `SSL` (bool), `Email` (string, optional – empty means `--register-unsafely-without-email`).
- [x] Consolidate all configuration maps into a single root structural type named `Config`.
- [x] Define runtime types: `Application{ Config *Config }` (compiled context passed to drivers) and `AppStatus{ Status, Uptime, SSLStatus }` (returned by `Status`).

### 1.2. Configuration Parser Engine (`internal/config/parser.go`)
- [x] Import `github.com/pelletier/go-toml/v2` (locked; NOT `BurntSushi/toml` – go-toml/v2 gives better struct tagging, strict type validation, and JSON-schema generation for the MCP `easydrop://docs/schema` resource).
- [x] Implement signature: `ParseConfig(path string) (*models.Config, error)`.
- [x] Enforce field validations: return explicit errors if `app.name` or `server.host` are omitted; `build.strategy` must be `"remote"`|`"local"`; `driver.type` must be `"solo"`|`"compose"`|`"swarm"`.
- [x] Implement fallback defaults for optional properties:
  - `server.port` = `22`
  - `server.ssh_key` = `~/.ssh/id_rsa` (ensure tilde `~` expansion to absolute system paths).
  - `build.strategy` = `"remote"`
  - `driver.type` = `"solo"`
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
- [x] Ship the compiled `project.tar.gz` bundle into isolated workspace storage on the host (`/tmp/easydrop/builds/[appName]/`) via `UploadFile` (parent dirs auto-created by both executors).
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

## §5. Solo Orchestration Driver Module

### 5.1. Runtime Deployment Implementation (`internal/core/drivers/solo.go`)
- [ ] Design `SoloDriver` conforming to the unified abstraction contract `DeploymentDriver`.
- [ ] Code the `Deploy(ctx context.Context, app *models.Application)` zero-downtime Blue-Green swap sequence:
- [ ] Evaluate host landscape state. Check for an active running instance named `[appName]-active`.
- [ ] Offset target port configurations to safely map staging environments (e.g., `app.Port + 1`).
- [ ] Assemble and execute the deployment instructions for the isolated staging ("green") instance:
  ```bash
  docker run -d --name [appName]-green -p [temp_port]:[app_internal_port] --restart unless-stopped easydrop/[appName]:latest
  ```
- [ ] Init local probing loops: execute networking diagnostics inside the host context hitting `http://localhost:[temp_port][health_check_path]` (path from `app.Config.App.HealthCheckPath`, default `"/"`) every 2 seconds (up to 10 total validation retries).
- [ ] If the internal probe captures a valid HTTP `200 OK` handshake, promote the container. Move to ingress traffic rerouting (Nginx updates).
- [ ] After routing swaps complete, cleanly shut down and purge deprecated host allocations:
  ```bash
  docker stop [appName]-active && docker rm [appName]-active
  ```
- [ ] Promote staging tags into permanent operational roles:
  ```bash
  docker rename [appName]-green [appName]-active
  ```
- [ ] Rollback Strategy: If internal network validation loops yield failures (timeouts), drop staging modifications (`docker stop [appName]-green && docker rm [appName]-green`) without interfering with production traffic, and terminate with an error status.

---

## §6. Ingress Network Management (Nginx & Certbot)

### 6.1. Reverse Proxy Provisioner Component (`internal/core/infra/nginx.go`)
- [ ] Compose embeddable base config structures inside `templates/nginx.conf.tmpl` backing reactive upstream updates.
- [ ] Bind variable states (such as `domain` and `target_port`) leveraging `text/template`.
- [ ] Deliver compiled reverse proxy settings by STAGING via `UploadFile` to `/tmp/easydrop/nginx/[domain]` first (locked decision – SFTP cannot write to `/etc/nginx` directly), then move atomically:
  ```bash
  sudo mv /tmp/easydrop/nginx/[domain] /etc/nginx/sites-available/[domain]
  ```
- [ ] Bind active deployment paths through symbolic mapping targets via `ExecCommand`:
  ```bash
  sudo ln -sf /etc/nginx/sites-available/[domain] /etc/nginx/sites-enabled/
  ```
- [ ] Run structural routing tests via `sudo nginx -t`. If validations match successfully, apply live settings: `sudo systemctl reload nginx`.

### 6.2. Cryptographic TLS Provisioner Component (internal/core/infra/certbot.go)
- [ ] Expose authorization endpoints: `EnableSSL(ctx context.Context, domain, email string) error`.
- [ ] Intercept local environment rules: if configuration metrics address `"localhost"` or `"127.0.0.1"`, exit early with a success code (Let's Encrypt does not offer domain validation flows across local network endpoints).
- [ ] If `email` is empty, provision with `--register-unsafely-without-email` (locked decision):
  ```bash
  sudo certbot --nginx -d [domain] --non-interactive --agree-tos --register-unsafely-without-email
  ```
  Otherwise:
  ```bash
  sudo certbot --nginx -d [domain] --non-interactive --agree-tos --email [email]
  ```

---

## §7. Single-Binary CLI (`cmd/easydrop`, Milestone 7)

- [ ] Single binary entry point `cmd/easydrop/main.go` (locked – no `cmd/cli` + `cmd/mcp-server` split; NFR-03).
- [ ] CLI framework `github.com/spf13/cobra` WITHOUT `viper` (locked – single `easydrop.toml`, cobra flags suffice).
- [ ] Commands from `docs/cli-spec.md`: `init [--force]`, `deploy [-c/--config] [--no-cache]`, `status`, `logs [app_name] [-f/--follow] [-n/--tail]`, `mcp-server` (hidden/explicit subcommand switching the binary into MCP stdio mode).

## §8. MCP Server (Milestone 8)

- [ ] Serve stdio JSON-RPC via `easydrop mcp-server` using the official `modelcontextprotocol/go-sdk` (locked).
- [ ] Tools map 1:1 to `docs/mcp-spec.md`: `init_project`, `deploy_app`, `get_status`, `get_logs`, `manage_server`; resources `easydrop://docs/schema` (generated from go-toml/v2 structs) and `easydrop://docs/troubleshooting`.
- [ ] `manage_server` persistence: MVP = local file store with `0600` permissions; encrypted vault is deferred to Milestone 9.
