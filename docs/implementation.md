# Technical Implementation Steps & Roadmap

This document outlines the chronological execution order, core data contracts, and specific acceptance criteria for each system module. Steps must be developed sequentially. Every micro-task includes a dedicated checkbox to explicitly track progress.

---

## §0. Sequence of Execution

Development is decoupled into isolated milestones. Proceeding to a subsequent milestone is strictly conditional upon 100% compilation and passing unit tests of the current phase.

- [ ] **Milestone 1:** Configuration Parsing Architecture (`internal/config` & `internal/models`)
- [ ] **Milestone 2:** Host Environment Abstraction Layer (`CommandExecutor`, `SSHExecutor`, `LocalExecutor`)
- [ ] **Milestone 3:** Target Environment Autonomic Bootstrapper (`Bootstrapper`)
- [ ] **Milestone 4:** Automated Source Compiling Engine (`RemoteBuilder`)
- [ ] **Milestone 5:** Solo Container Orchestration Driver (`SoloDriver` with Blue-Green logic)
- [ ] **Milestone 6:** Ingress Networking Infrastructure Layer (`NginxManager` & `CertbotManager`)

---

## §1. Configuration Blueprint & Domain Models

### 1.1. Data Struct Modeling (`internal/models/types.go`)
- [ ] Define `AppConfig` for the `[app]` block: `Name` (string), `Port` (int).
- [ ] Define `ServerConfig` for the `[server]` block: `Host` (string), `User` (string), `SSHKey` (string), `Password` (string), `Port` (int).
- [ ] Define `BuildConfig` for the `[build]` block: `Strategy` (string).
- [ ] Define `DriverConfig` for the `[driver]` block: `Type` (string).
- [ ] Define `NginxConfig` for the `[nginx]` block: `Domain` (string), `SSL` (bool), `Email` (string).
- [ ] Consolidate all configuration maps into a single root structural type named `Config`.

### 1.2. Configuration Parser Engine (`internal/config/parser.go`)
- [ ] Import external module `://github.com`.
- [ ] Implement signature: `ParseConfig(path string) (*models.Config, error)`.
- [ ] Enforce field validations: return explicit errors if `app.name` or `server.host` are omitted.
- [ ] Implement fallback defaults for optional properties:
  - `server.port` = `22`
  - `server.ssh_key` = `~/.ssh/id_rsa` (ensure tilde `~` expansion to absolute system paths).
  - `build.strategy` = `"remote"`
  - `driver.type` = `"solo"`

### 1.3. Parsing Test Suite (`internal/config/parser_test.go`)
- [ ] Test Case: Parsing a fully populated valid TOML template.
- [ ] Test Case: Asserting initialization failure when critical parameters are missing.
- [ ] Test Case: Fallback mapping verification for missing optional attributes.

---

## §2. Command Execution Abstraction (Executor)

### 2.1. Interface Architecture Blueprint (`internal/core/executor.go`)
- [ ] Declare the `CommandExecutor` contract:
  ```go
  type CommandExecutor interface {
      ExecCommand(ctx context.Context, cmd string) (stdout, stderr string, exitCode int, err error)
      UploadFile(ctx context.Context, srcPath, destPath string) error
  }
  ```

### 2.2. Native Environment Client (`internal/core/local_client.go`)
- [ ] Write `LocalExecutor` implementation satisfying `CommandExecutor`.
- [ ] Implement `ExecCommand`: route inputs through native `os/exec.CommandContext`, safely grabbing `stdout`, `stderr`, and evaluating structural exit codes.
- [ ] Implement `UploadFile`: map file streams natively using `os.ReadFile`, `os.WriteFile`, or `io.Copy` targeting local scopes. Assign file operational permissions to `0644`.

### 2.3. Cryptographic Remote Tunnel Client (`internal/core/ssh_client.go`)
- [ ] Write `SSHExecutor` implementation satisfying `CommandExecutor`.
- [ ] Import dependencies `golang.org/x/crypto/ssh` and `://github.com`.
- [ ] Wire multi-authentication backing: handle both standard private cryptographic keys (with `ssh-agent` discovery) and pure fallback password strings.
- [ ] Implement `ExecCommand`: allocate an active `ssh.Session`, pipe input commands, and parse the buffer output.
- [ ] Sanitize and escape all input command strings before routing to prevent shell injection vectors.
- [ ] Implement `UploadFile`: provision an SFTP connection using `sftp.NewClient`, create files on the remote filesystem, and pipe raw bytes.

### 2.4. Environment Factory Router (`internal/core/factory.go`)
- [ ] Write initialization driver logic:
  ```go
  func NewExecutor(cfg *models.ServerConfig) (CommandExecutor, error)
  ```
- [ ] Intercept routing conditions: if `cfg.Host` targets `"localhost"` or `"127.0.0.1"`, bypass socket creation entirely and instantiate a `LocalExecutor`. For all external destinations, provision an `SSHExecutor`.

---

## §3. Environment Autonomic Bootstrapper

### 3.1. Infrastructure Inspection Pipeline (`internal/core/bootstrapper/bootstrap.go`)
- [ ] Write `Bootstrapper` logic consuming a generic `CommandExecutor`.
- [ ] Expose entry execution contract: `Bootstrap(ctx context.Context) error`.
- [ ] OS Validation: Read `/etc/os-release`. Halt execution if `ID=ubuntu` or `ID=debian` matches fail. (If executing via `LocalExecutor` on macOS/Windows environments, emit a soft diagnostic alert to `stderr` and proceed).
- [ ] Container Runtime Verification: Assert `docker --version`. If an invocation error returns, push automated install routines:
  ```bash
  curl -fsSL https://docker.com -o get-docker.sh && sh get-docker.sh
  ```
- [ ] Orchestration Utility Verification: Assert `docker compose version`. If missing, inspect target platform hardware architecture via `uname -m`. Fetch the appropriate binary from upstream GitHub Releases (`://github.com`), write into `~/.docker/cli-plugins/docker-compose`, and flags execution rights to `0755`.
- [ ] Security Privileges Step: Append user scopes to runtime groups: `sudo usermod -aG docker $USER`.
- [ ] Firewall Provisioning: Detect systemic presence of `ufw`. If active, ensure traffic rule alignment:
  ```bash
  sudo ufw allow OpenSSH
  sudo ufw allow 'Nginx Full'
  sudo ufw --force enable
  ```

---

## §4. Host Compilation Subsystem (Remote Builder)

### 4.1. File System Compressor Component (`internal/core/builder/archive.go`)
- [ ] Construct file directory scanning helpers.
- [ ] Parse project `.dockerignore` filters if present. Explicitly discard path patterns matching `.git`, `node_modules`, compiled build binaries, and the target archive bundle `project.tar.gz`.
- [ ] Implement archive consolidation to compress files using `archive/tar` and `compress/gzip`.

### 4.2. Pipeline Ship & Build Orchestrator (`internal/core/builder/build.go`)
- [ ] Create `RemoteBuilder` containing an active `CommandExecutor`.
- [ ] Declare pipeline signature: `Build(ctx context.Context, appName string) error`.
- [ ] Run local workspace compression workflows.
- [ ] Ship the compiled `project.tar.gz` bundle into isolated workspace storage on the host (`/tmp/easydrop/builds/[appName]/`) via `UploadFile`.
- [ ] Trigger remote file unpack sequences via `ExecCommand`:
  ```bash
  tar -xzf /tmp/easydrop/builds/[appName]/project.tar.gz -C /tmp/easydrop/builds/[appName]/
  ```
- [ ] Fire runtime container image compiler jobs on the host terminal scope:
  ```bash
  docker build -t easydrop/[appName]:latest /tmp/easydrop/builds/[appName]/
  ```
- [ ] Purge temporary environment file footprint `/tmp/easydrop/builds/[appName]/` upon successful build execution.

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
- [ ] Init local probing loops: execute networking diagnostics inside the host context hitting `http://localhost:[temp_port]` every 2 seconds (up to 10 total validation retries).
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
- [ ] Deliver compiled system reverse proxy settings onto systemic network paths `/etc/nginx/sites-available/[domain]` via `UploadFile`.
- [ ] Bind active deployment paths through symbolic mapping targets via `ExecCommand`:
  ```bash
  sudo ln -sf /etc/nginx/sites-available/[domain] /etc/nginx/sites-enabled/
  ```
- [ ] Run structural routing tests via `sudo nginx -t`. If validations match successfully, apply live settings: `sudo systemctl reload nginx`.

### 6.2. Cryptographic TLS Provisioner Component (internal/core/infra/certbot.go)
- [ ] Expose authorization endpoints: `EnableSSL(ctx context.Context, domain, email string) error`.
- [ ] Intercept local environment rules: if configuration metrics address `"localhost"` or `"127.0.0.1"`, exit early with a success code (Let's Encrypt does not offer domain validation flows across local network endpoints).
- [ ] Dispatch certificate provisioning commands on the host machine:
  ```bash
  sudo certbot --nginx -d [domain] --non-interactive --agree-tos --email [email]
  ```
