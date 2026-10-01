# Technical Implementation Steps & Roadmap

This document outlines the chronological execution order, core data contracts, and specific acceptance criteria for each system module. Steps must be developed sequentially. Every micro-task includes a dedicated checkbox to explicitly track progress.

> **Developer workflow:** the repo ships a `Makefile` – `make` lists all targets, `make build` compiles the binary (version stamped from `git describe`), `make verify` is the CI gate (gofmt + `go vet` + tests), `make test-race` runs the race detector, `make cover` writes `bin/coverage.out`, `make release` cross-compiles all six platform binaries with `SHA256SUMS` (NFR-03), and `make smoke` deploys a throwaway app to the local docker daemon end-to-end. See README § Development.

---

## §0. Sequence of Execution

Development is decoupled into isolated milestones. Proceeding to a subsequent milestone is strictly conditional upon 100% compilation and passing unit tests of the current phase.

- [x] **Milestone 1:** Configuration Parsing Architecture (`internal/config` & `internal/models`) – DONE. Stack: `github.com/pelletier/go-toml/v2`.
- [x] **Milestone 2:** Host Environment Abstraction Layer – DONE (`CommandExecutor` with `Close()`, `SSHExecutor`, `LocalExecutor`, factory; `internal/core/*_test.go` green).
- [x] **Milestone 3:** Target Environment Autonomic Bootstrapper – DONE (`Bootstrapper` + runtime tag/arch resolution; `bootstrap_test.go` green).
- [x] **Milestone 4:** Automated Source Compiling Engine – DONE (`RemoteBuilder` + tar.gz archiver with .dockerignore; `LocalBuilder` delivered in M11).
- [x] **Milestone 5:** Single Container Orchestration Driver – DONE (`SingleDriver` direct default + opt-in Blue-Green on the {Port, Port+1} pair, backup + `Rollback`, Status/Logs/Teardown; `drivers/single_test.go` green).
- [x] **Milestone 6:** Ingress Networking Infrastructure Layer – DONE (`NginxManager.Apply/UpdateIngress` + `CertbotManager.EnableSSL`; `infra/*_test.go` green).
- [x] **Milestone 7:** Single-binary CLI – DONE (`cmd/easydrop` + `internal/cli` + `config.Scaffold`; real localhost deploy/status/logs/BG/rollback verified via built binary).
- [x] **Milestone 8:** MCP Server – DONE (`internal/mcp` on official SDK + shared `internal/deploy` core; handler/store/e2e tests + live stdio smoke green).
- [x] **Milestone 9:** Compose/Swarm drivers, encrypted server vault, snap-docker fail-fast (OD-02), generated JSON-Schema resource – DONE.
- [x] **Milestone 10:** Port split `app.port` / `app.host_port` (resolves OD-01) – DONE (config defaults + validation, SingleDriver pair, schema meta, live smoke on a fixed-port image).
- [x] **Milestone 11:** `LocalBuilder` – registry strategy `build.strategy = "local"` (resolves OD-00, FR-04) – DONE (`builder/local.go`, deploy wiring, live smoke against a real `registry:2`).
- [x] **Milestone 12:** `teardown` exposed in both interfaces (`easydrop teardown`, `teardown_app`) – DONE (`deploy.Teardown` routes to the configured driver; volumes are never deleted; live deploy→teardown→re-teardown smoke green).

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
- [x] Define `AppConfig` for the `[app]` block: `Name` (string), `Port` (int, in-container listen port), `HostPort` (int, host-published port, default = `Port`, M10), `HealthCheckPath` (string, default `"/"`).
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
  - `app.host_port` = `app.port` (M10: omitted `host_port` keeps pre-M10 behavior)
  - Port validation: `app.port` and `app.host_port` in 1-65535, and `app.host_port` ≤ 65534 (the Blue-Green pair needs `host_port+1`)

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
- [x] Ship the compiled `project.tar.gz` bundle into isolated workspace storage on the host (`[stagingBase]/builds/[appName]/`, default base `/tmp/easydrop`) via `UploadFile` (parent dirs auto-created by both executors). Escape hatch (locked): env `EASYDROP_STAGING_BASE` overrides the base for hosts where `/tmp` is unsuitable (tiny tmpfs, noexec, snap-confined daemons – see §10 and OD-02 in §9).
- [x] Trigger remote file unpack sequences via `ExecCommand` (paths quoted via `core.EscapeShellArg`):
  ```bash
  tar -xzf /tmp/easydrop/builds/[appName]/project.tar.gz -C /tmp/easydrop/builds/[appName]/
  ```
- [x] Fire runtime container image compiler jobs on the host terminal scope (append `--no-cache` when the effective no-cache flag is set):
  ```bash
  docker build -t easydrop/[appName]:latest /tmp/easydrop/builds/[appName]/
  ```
- [x] Purge temporary environment file footprint `/tmp/easydrop/builds/[appName]/` (`rm -rf`, quoted) upon successful build execution. On build failure the staging dir is intentionally kept for debugging.
- [x] `LocalBuilder` (FR-04, delivered in **M11**, see §4.3): build locally, push to `build.registry`, pull on the target host.

### 4.3. Local Builder – registry strategy (`internal/core/builder/local.go`, Milestone 11)
- [x] `LocalBuilder{ SrcDir, Out, CommandTimeout }` runs `docker build → push` on the machine executing EasyDrop (NOT the target): the image crosses the wire, the workspace never does. `CommandTimeout` defaults to 30m for large builds.
- [x] `TargetRef(cfg) (string, error)` resolves `[registry/]<image>:<tag>`: `build.image` may be `name`, `name:tag` or `ns/name:tag`; registry is prepended, trailing `/` trimmed, tag defaults to `latest`. Validation: `build.registry` required and scheme-free (`https://` rejected – docker refs never carry one), registry/repo/tag charset-checked, app name validated.
- [x] `build.no_cache` / `deploy --no-cache` adds `--no-cache` to the local build too.
- [x] `checkLocalStaging`: snap-confined daemons cannot see `/tmp` build contexts (OD-02) – fails fast with an actionable message instead of the daemon's opaque `failed to read dockerfile`.
- [x] `deploy.Run` wiring: with `build.strategy = "local"` the remote `RemoteBuilder` step is SKIPPED, `app.Image` is pinned to the pushed ref, and the target runs `docker pull <ref>` before the driver deploys. Rejected for compose/swarm with an explicit error (those build their services on the host). `SingleDriver.imageRef(app)` returns `app.Image` when set, otherwise `easydrop/<name>:latest`.
- [x] Verified: real `registry:2` on :15500 – local build → push → pull on target → container reported `localhost:15500/localsmoke/localsmoke:latest` as its image, `status` Up, curl answered. Registry, images, containers and scratch dirs removed afterwards.

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
- [x] Port scheme (locked): only the pair `{hostPort, hostPort+1}` is ever used, where `hostPort = app.host_port` (defaults to `app.port` – see §1.1). Green stages on whichever is free (active's port + alternation: no active → `hostPort`; active on `hostPort` → `hostPort+1`; active on `hostPort+1` → `hostPort`). Generalizes the spec's `app.Port + 1` example and prevents port leaks. Containers always listen on the internal `app.port`, so fixed-port images (`nginx:80`) publish anywhere (M10 / OD-01).
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

## §5A. Compose & Swarm Drivers (FR-06/FR-07, Milestone 9)

Shared for both drivers:
- [x] Workspace shipping is the same archiver as M4: `builder.ArchiveWorkspace` + `builder.StageWorkspace` (compose/swarm build on the host, so no `RemoteBuilder` step – `deploy.Run` branches on driver type).
- [x] Persistent state dir resolved ONCE per driver instance via `printf %s "$HOME"` (`resolveHome`) – never `~` or a quoted `$HOME` inside a path (both break under snap confinement; see OD-02). Layout: `$HOME/easydrop/apps/[app]` on snap hosts, `$HOME/.easydrop/apps/[app]` elsewhere (`stateDir`).
- [x] `Logs` reuses the shared `streamLines` helper (dedup window 500, ctx-aware polling) extracted from the single driver.
- [x] Both implement the full `DeploymentDriver` contract incl. `Rollback` on the previous compose/stack file; backup is consumed on rollback and refreshed only by a replacing deploy.

### 5A.1. Compose driver (`internal/core/drivers/compose.go`)
- [x] `Deploy`: ship workspace → `docker compose -p [project] -f [file] up -d --build --remove-orphans` → verify EVERY service `running` (`compose ps --format '{{.Service}}|{{.State}}'`) → persist `compose.yml` (backing up the replaced one to `compose.previous.yml`, plus `.env` when present) → purge staging → ingress update. Non-running services abort BEFORE persisting; staging is kept for debugging.
- [x] `Status`: all `running`→`Up`, any `restarting`→`Restarting`, else `Down`; uptime from `compose ps` Status column; missing deployment → `Down` (not an error).
- [x] `Logs`: `compose logs --tail [n]` across all services (service-prefixed lines).
- [x] `Rollback`: `test -f compose.previous.yml` (fail fast, zero mutations) → `cp` back → `up -d` WITHOUT `--build` (images are cached) → re-verify running → consume backup.
- [x] `Teardown`: `compose down` (no `-v` – named volumes are never deleted) + remove the state dir; idempotent.

### 5A.2. Swarm driver (`internal/core/drivers/swarm.go`)
- [x] MVP scope: single-node swarm only. `ensureSwarm` reads `{{.Swarm.LocalNodeState}}` and runs `docker swarm init` when inactive; multi-node joins are out of scope (reported in code comments, not silently mis-handled).
- [x] `Deploy`: ship workspace → `docker compose -f [file] build` (service images on the node) → `docker stack deploy -c [file] [stack]` → verify every service at full replicas (`x/x` via `docker stack services --format '{{.Replicas}}'`) → persist stack file + backup → purge staging → ingress.
- [x] `Status`: all services at full replicas → `Up`, otherwise `Down`; missing deployment → `Down`.
- [x] `Logs`: per-service `docker service logs --tail [n]` with `==> [service] <==` headers.
- [x] `Rollback` (previous stack file, no rebuild) and `Teardown` (`docker stack ls` membership check → `docker stack rm` → remove state dir); both idempotent.
- [x] Registry-based image distribution for multi-node clusters is out of MVP scope.

### 5A.3. Driver selection (`internal/deploy/deploy.go`)
- [x] `NewDriver(cfg, ex, srcDir)` switches on `driver.type` → `SingleDriver` / `ComposeDriver` / `SwarmDriver`, wiring ingress when `nginx.domain` is set; unknown type errors out. `Status`/`Logs`/`Rollback` all route through it (no driver hardcoding left in the interface layer).
- [x] Blue-Green is rejected for compose/swarm up front: `blue-green is only supported by the single driver`.
- [x] Verified: live Compose smoke on the snap-docker dev host – `init` → `deploy` (2 services) → `status` (`Up`) → `logs` (real service output) → re-deploy (backup created) → `rollback` (restored, backup consumed) → teardown; containers, images and state dirs cleaned afterwards.

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
- [x] Commands from `docs/cli-spec.md`: `init [--force]` (via `config.Scaffold`+`WriteConfig`), `deploy [-c/--config] [--no-cache] [--blue-green] [--skip-bootstrap]`, `status`, `logs [app_name] [-f/--follow] [-n/--tail]`, `rollback [app_name]`, `teardown [app_name]` (M12). `mcp-server` lands in Milestone 8.
- [x] `deploy` pipeline (`runDeploy`): parse → overlay `--no-cache`/`--blue-green` (`applyDeployFlags`, never unsets config-true) → gate `driver.type == single` → signal-aware ctx → `NewExecutor` → Bootstrap (skipped with `--skip-bootstrap`) → `Build` with `SrcDir` = config file's directory → `SingleDriver` (`BlueGreen` from config, `Ingress` wired when domain non-empty) → `Deploy` → Certbot when `nginx.ssl && domain != ""` (manager no-ops on localhost).
- [x] `init` scaffolding (`config.Scaffold`, FR-02): EXPOSE port from Dockerfile (default 8080), compose driver on compose files, app name = sanitized dir base, server = localhost + current user. `WriteConfig` refuses overwrite without `--force`; output round-trips through `ParseConfig` (tested).
- [x] Cross-field note (locked): `Bootstrapper.ensureDockerGroup` checks `id -nG` membership first and skips `usermod` when already in the docker group (idempotent, avoids pointless sudo).

## §8. MCP Server (Milestone 8)

- [x] Serve stdio JSON-RPC via `easydrop mcp-server` using the official `modelcontextprotocol/go-sdk` v1.8.0 (locked). `internal/mcp/server.go`: `NewServer()` + `Run(ctx)` over `mcp.StdioTransport{}`; `internal/cli/mcpserver.go` wires the cobra subcommand with signal-aware ctx. Single `internal/version.Version` (`0.1.0`, ldflags-overridable) feeds both `--version` and MCP `serverInfo`.
- [x] One core, two interfaces (locked): the deploy pipeline lives in `internal/deploy/` (`Options`, `Run`, `LoadConfig`, `ResolveAppName`, `ApplyFlags`, `Status`, `Logs` with collection cap, `Rollback`, `Init`); `internal/cli` commands are thin wrappers, MCP handlers call the same functions. No CLI↔MCP imports.
- [x] Tools map 1:1 to `docs/mcp-spec.md`: `init_project`, `deploy_app`, `get_status`, `get_logs`, `rollback_app`, `teardown_app` (M12), `manage_server`; resources `easydrop://docs/schema` (**generated** by reflection over `models.Config` – `schemaMeta` supplies descriptions/defaults/enums, missing entry = error, so schema can never drift from the parser) and `easydrop://docs/troubleshooting` (static runbook). Typed I/O with `jsonschema` tags; app failures return `isError` tool results (not protocol errors); `follow` log collection capped at 2000 lines.
- [x] `manage_server` persistence: **encrypted** vault `~/.easydrop/servers.vault` – AES-256-GCM, scrypt `(N=32768, r=8, p=1)`, dir `0700`, file `0600`, upsert by host+user. Key material comes only from `EASYDROP_VAULT_PASSWORD` (no prompt – MCP is non-interactive; fail closed). Legacy plaintext `servers.toml` is imported once and renamed `servers.toml.migrated`. `EASYDROP_SERVERS_FILE` overrides the path. Secrets never echoed in messages or logs (tested).
- [x] Verified: unit tests (store round-trip/perms/validation, handler arg validation, schema registration) + in-process e2e with a real SDK client (`e2e_test.go`: tools/list, init→EXPOSE detect, manage add + secret-leak check, resources, status-without-config isError) + live stdio smoke against the built binary (initialize, tools/list, init_project, manage_server, get_status error path, resources/list+read).

---

## §9. Open Decisions (require a product call – found during M4/M5 smoke tests)

### OD-00: Local build strategy (FR-04)
- **Context (original):** `build.strategy = "local"` parsed fine, but only
  `RemoteBuilder` existed, so registry-based builds were impossible.
- **Decision (M11, option B):** implemented `LocalBuilder` – build on the
  machine running EasyDrop, push to `build.registry`, pull on the target host.
  Useful for CI runners without SSH, air-gapped targets, and shared registries.
  Scope: `single` driver only (compose/swarm build on the host and are
  rejected with an explicit error for `local`).
- **Status:** RESOLVED in M11. See §4.3.

### OD-01: Split container-internal vs host-published ports?
- **Context (original):** `app.port` played two roles – the container's internal
  listen port AND the managed host-port pair `{Port, Port+1}` (SingleDriver §5).
  An image with a fixed internal port (e.g. `nginx:80`) could not be published
  on a different host port (host 80 busy → `address already in use`), and the
  app would land directly on a public port, bypassing the Nginx/TLS layer.
- **Decision (M10, option B):** added optional `app.host_port` (default =
  `app.port`). `port` is now only the in-container listen port; the managed
  Blue-Green pair is `{host_port, host_port+1}`. Backward compatible: configs
  without `host_port` behave exactly as before.
- **Status:** RESOLVED in M10. Validation: both ports must be 1-65535, and
  `host_port` ≤ 65534 (the pair needs `host_port+1`). Implemented in
  `ApplyDefaults` (`internal/config`), `publishedPort` +
  `deployBlueGreen`/`deployDirect` (SingleDriver), and the generated MCP
  schema (`schemaMeta` entry `app.host_port`).
- **Verified:** live smoke with a fixed-port image (`http-echo` on :80 inside,
  published as `18095:80`): direct deploy, Blue-Green swap onto `18096` keeping
  internal 80, and rollback back to 18095 – all green via the built binary.

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
- **Status:** RESOLVED in M9 (option **B-lite**). `builder.IsSnapConfinedDaemon`
  probes `docker info` once; with `/tmp`-based staging it aborts early with
  the actionable `EASYDROP_STAGING_BASE` hint instead of the daemon's opaque
  `path ... not found`. Second snap limitation found during the M9 Compose
  smoke and also handled: snapd's home interface blocks hidden files in
  `$HOME`, so the compose/swarm state dir is `$HOME/easydrop/apps` (no dot)
  on snap hosts and `$HOME/.easydrop/apps` elsewhere.
- **Verified:** snap host (this dev box) – Compose deploy → status → logs →
  re-deploy (backup created) → rollback, all green with the non-hidden state
  path.

---

## §10. Snap-confined Docker – known limitations & handling

Found empirically on the dev box (Ubuntu 24.04 + `snap` Docker 29.8.0, `DockerRootDir=/var/snap/docker/common/var-lib-docker`). A snap-confined `dockerd` runs in its own mount namespace, so paths the host process sees are **not** necessarily visible to the daemon. Three distinct limits bit us; all are now handled explicitly.

### 10.1 The three sandbox limits

| # | Limit | Symptom (raw daemon error) | Where it bites |
|---|-------|----------------------------|----------------|
| 1 | Private `/tmp` | `docker build` → `unable to prepare context: path "/tmp/easydrop/builds/<app>" not found` | Remote staging + build (§4.2) |
| 2 | No access to hidden (`dot-`) files/dirs in `$HOME` | `compose` → `open /home/<u>/.easydrop/apps/<app>/compose.yml: permission denied`; also `failed to read dockerfile` when the build context is under a dot-dir | Compose/Swarm state dir (§5A), local build context (§4.3) |
| 3 | Shell expansion is not the daemon's business | `docker compose -f '$HOME/...'` → `"/var/lib/snapd/void/$HOME/..."` (snap runs commands with a synthetic `HOME`; single quotes also block shell expansion) | Any path built from `$HOME` inside a composed command (§5A) |

Limit 3 is the subtle one: it is *not* a permission problem, it is two different expansion contexts – the shell's `$HOME` (where the file really is) vs the daemon's `HOME` (a snapd void path) – so the fix is to resolve `$HOME` once via `printf %s "$HOME"` and pass an absolute path.

### 10.2 How EasyDrop handles it

- **Detection** (single probe, shared): `builder.IsSnapConfinedDaemon` runs `docker info --format '{{.DockerRootDir}}|{{.OperatingSystem}}'` and flags `/var/snap/docker` or `Ubuntu Core`. Unparsable output → treated as non-snap (the next docker call fails loudly on its own).
- **Staging** (§4.2/§4.3): when snap + `/tmp`-based staging, both builders abort early with an actionable hint instead of the opaque `path … not found` / `failed to read dockerfile`.
- **State dir** (§5A): `$HOME/easydrop/apps/<app>` (no leading dot) on snap hosts, `$HOME/.easydrop/apps/<app>` elsewhere – `stateDir()`.
- **Home resolution** (§5A): `resolveHome` executes `printf %s "$HOME"` once per driver instance and rejects snap's `/var/lib/snapd/void` result, instead of embedding `'$HOME/...'` in commands.
- **Escape hatch**: `EASYDROP_STAGING_BASE` picks any daemon-visible directory. On snap hosts it must be **non-hidden** (limit 2).

### 10.3 Operator guidance

```bash
# on a snap-docker host, before any easydrop command that ships a workspace:
export EASYDROP_STAGING_BASE=~/easydrop-staging   # non-hidden, daemon-visible
```

`make smoke` performs this detection and re-pointing automatically.

### 10.4 Scope & non-goals

- Hosts prepared by our own `Bootstrapper` install Docker via `get.docker.com` (apt) and are therefore **never** affected – snap only matters when EasyDrop targets a pre-existing snap-docker host (dev boxes, some managed images).
- Installing Docker from snap instead of apt, or relaxing the snap confinement, is explicitly out of scope: the mitigation above keeps EasyDrop working without changing the host's packaging.
- Verified end-to-end on a snap host: remote build + Compose deploy → status → logs → re-deploy → rollback → teardown, and `build.strategy = "local"` against a real `registry:2`.
