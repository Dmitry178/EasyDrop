# Technical Implementation Steps & Roadmap

This document outlines the chronological execution order, core data contracts, and specific acceptance criteria for each system module. Steps must be developed sequentially. Every micro-task includes a dedicated checkbox to explicitly track progress.

> **Developer workflow:** the repo ships a `Makefile` – `make` lists all targets, `make build` compiles the binary (version stamped from `git describe`), `make verify` is the CI gate (gofmt + `go vet` + tests), `make test-race` runs the race detector, `make cover` writes `bin/coverage.out`, `make release` cross-compiles all six platform binaries with `SHA256SUMS` (NFR-03), and `make smoke` deploys a throwaway app to the local docker daemon end-to-end. See README § Development.
>
> **Continuous integration:** `.github/workflows/ci.yml` runs `verify` (incl. `make check-platforms`), a real `smoke`, one `build` job per platform triple, native test runs on Linux/macOS/Windows, and – on a `v*` tag – a `release` job that builds every binary plus `SHA256SUMS` and publishes the GitHub release from them (`contents: write` scoped to that job alone). Debugging needs no tag at all: a plain push to `main` runs everything except `release`.
>
> **Two drift guards, both deliberately cheap and both before any artifact exists.** `PLATFORMS` in the `Makefile` is the single source of truth and the workflow repeats it; `make check-platforms` fails `verify` on drift, so a release cannot quietly stop building for one platform. `make check-version EXPECTED=<tag>` is the first step of `release`: a tag whose number disagrees with `internal/version.Version` or with the `CHANGELOG.md` heading is refused. Both lists are hand-maintained next to code they describe, which is exactly the arrangement that drifts silently otherwise.

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
- [x] **Milestone 13:** secrets out of `easydrop.toml` – `${VAR}` interpolation in the string fields + optional `.easydrop.env`/`.env` next to the config – DONE (`internal/config/env.go`; resolves OD-03; the dotenv files are excluded from the shipped archive; NFR-02 covered by a dedicated no-leak test).
- [x] **Milestone 14:** `init` scans the project the way the docs always promised (FR-02) – Dockerfile EXPOSE, compose `ports`/`expose`, `package.json` (scripts + framework), language manifests and a bounded source scan, with the detected source reported for every value and **no port invented when detection comes up empty** (OD-04) – DONE (`internal/config/detect.go`, `ScaffoldResult`; see §1.4 and OD-04; also fixes the `compose.yml` → `compose_file` mismatch).
- [x] **Milestone 15:** the port can be supplied explicitly (`easydrop init --port N`, `init_project {port: N}`) and both interfaces share one report text, so an agent receives the same facts and the same method as a human – DONE (`internal/config/report.go`, `ScaffoldOptions`, `deploy.InitOptions`; tests over a real MCP session).
- [x] **Milestone 16:** the MCP surface tells an agent what it may touch, and a failing container can be diagnosed without flooding the context window – DONE (explicit `ToolAnnotations` on all 7 tools, §8; `deploy.LogOptions{Filter}` in the shared core, exposed as `get_logs {filter}` and `logs --grep`, §7; `scanned`/`matched` counts so "0 matched" is never read as "healthy"; `Scope and Limitations` in both READMEs). Details in §7 and §8.
- [x] **Milestone 17:** a busy host port fails as a pre-flight instead of as a Docker networking error, and the two hand-maintained references can no longer drift from the code – DONE (`ensurePortFree` before every `docker run`, §5.1; an unreachable `SSH_AUTH_SOCK` names itself in the auth error instead of degrading to "no ssh auth methods", §2.3 + `docs/mcp-spec.md` §0.1; the `examples/README.md` key table is checked against `models.Config` by reflection, §1.3; the source-scan walk limits are locked by tests, §1.4).
- [x] **Milestone 18:** TLS for local and internal hosts – a self-signed leaf that easydrop generates, installs and serves, so `nginx.ssl = true` stops being a silent no-op on `localhost` – DONE (`nginx.self_signed`, §6.2A; `infra.SelfSignedCertifier`, pure-Go P-256 with SANs, §6.3; `templates/nginx-tls.conf.tmpl` serving `:80` and `:443`, §6.1).
- [x] **Milestone 19:** `teardown` leaves nothing serving – the managed nginx vhost goes with the containers, `status` finally reports TLS, and the self-signed certificate is purged only on request – DONE (`NginxManager.RemoveIngress` + `TLSStatus` from `nginx -T`, §6.1; `SelfSignedCertifier.RemoveCert`, §6.2A; `deploy.Teardown(TeardownOptions{Purge})` returning a `TeardownResult`, §5.4; `--purge` / `teardown_app {purge}`). Details in §5.2, §5.4 and §6.1.
- [x] **Milestone 20:** TLS is understood in both modes and by both instruments – the deploy probe can talk to an app that answers only over HTTPS, and `status` reports the expiry of the certificate nginx actually serves, in either mode – DONE (`app.health_check_scheme` plus the auto HTTP→HTTPS fallback in `probe`, §5.1; `TLSStatus` parses the served certificate instead of trusting config or the mode, §6.1; `SelfSignedCertifier.Describe` removed as superseded). Details in §5.1 and §6.1.
- [x] **Milestone 21:** the CI matrix stopped being a source of false confidence and a source of real bugs – the Windows and macOS native test jobs run, the four portability defects they exposed on their first run are fixed, and `check-version` makes a mismatched tag a build failure rather than a mystery. See §10A.

> Locked decisions (see ARCHITECTURE.md §5): single binary `cmd/easydrop/main.go`;
> TOML `github.com/pelletier/go-toml/v2`; CLI `cobra` (no `viper`);
> MCP official `modelcontextprotocol/go-sdk`;
> `Logs(ctx, appName, lines, follow) (<-chan string, error)` (driver level; the
> core wrapper takes `deploy.LogOptions` and adds `Filter`, M16);
> `CommandExecutor` always has `Close() error`;
> Nginx upload = stage to `/tmp/easydrop/` + `sudo mv`;
> Certbot without email = `--register-unsafely-without-email`.
>
> Cross-compilation: `CGO_ENABLED=0` everywhere, so one Linux runner produces every
> target with no cross toolchain; the CI matrix mirrors `PLATFORMS` and is kept in
> sync by `make check-platforms` (one build path: `build-platform` is what both the
> matrix and `release` call).
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
  - A **missing** `app.port` (0) is rejected with its own message, not the range one (OD-04): `app.port` carries `omitempty` so `init` can omit it, and a config without it must stop the deploy rather than silently become 8080
- [x] M13: resolve `${VAR}` references in the string fields before the required-field checks and before the defaults above (so a `${VAR:-~/.ssh/id_rsa}` default still gets its `~` expanded). Implementation and locked semantics: §9 OD-03.

### 1.2A. Secret Resolution (`internal/config/env.go`, M13)
- [x] Read the optional secret files next to the config, in increasing priority: `.env`, `.easydrop.env`. A missing file is not an error; both are git-ignored and excluded from the shipped archive (`internal/core/builder/archive.go`, `defaultExcludes`).
- [x] Precedence: real process environment > `.easydrop.env` > `.env`. Sources are composed through a `lookupFunc` – **never** `os.Setenv`, so a secret cannot leak into the inherited environment of the child `docker build` / `docker push` processes.
- [x] `${VAR}` = required (unset or empty → hard error naming field and variable, never the value), `${VAR:-default}` = default when unset/empty, `$$` = literal `$`, unterminated `${` and malformed names are errors. Defaults and dotenv values are literal: no nested expansion.
- [x] Expansion whitelist (explicit list, not reflection): `app.name`, `app.health_check_path`, `server.host`, `server.user`, `server.ssh_key`, `server.password`, `build.registry`, `build.image`, `driver.compose_file`, `nginx.domain`, `nginx.email`. Numbers and booleans are never interpolated.
- [x] Lenient dotenv parsing (`KEY=VALUE`, optional `export`, one layer of quote stripping, no escape interpretation): a `.env` shared with docker compose may contain syntax we do not model, and an unreadable line must not break a deploy. A misspelled key still fails loudly at the point of use, because `${MISSING}` is a hard error.

### 1.3. Parsing Test Suite (`internal/config/parser_test.go`, `internal/config/env_test.go`, `internal/config/examples_test.go`)
- [x] Test Case: Parsing a fully populated valid TOML template.
- [x] Test Case: Asserting initialization failure when critical parameters are missing.
- [x] Test Case: Fallback mapping verification for missing optional attributes.
- [x] Test Case: `${VAR}` resolution – set / unset / empty / `:-default` / `$$` escape / unterminated / bad name, plus the `env > .easydrop.env > .env` precedence and the interaction with `~` expansion.
- [x] Test Case (NFR-02): an error from a missing variable names the field and the variable but never echoes the value.
- [x] Test Case: every file in `examples/` parses through the real parser, uses only known section/key names, and demonstrates the case it is registered for.
- [x] Test Case (M17, `examples_docs_test.go`): the **key reference table in `examples/README.md`** matches `models.Config` exactly, by reflection over the same struct tags the parser decodes with. The table is prose-by-hand, so it drifts in both directions, and both matter: a key added to the config is invisible in the docs a user reads (while working fine in the parser and the generated MCP schema), and a removed key lingers as advice to set something that does nothing. Also pins duplicates, a second way for one row to silently shadow another.
- [x] Test Case (M14, `detect_test.go`): one fixture per detection source (EXPOSE, last-stage EXPOSE, compose short/long/protocol/ephemeral forms, preferred-service choice, `package.json` script and framework, source scan per pattern), the precedence rules, the skip lists (`node_modules`/`vendor` must not contribute), the non-port numbers that must not match, all four compose file spellings, stack detection, app-name fallback for generic directories, and garbage input degrading to the default.

### 1.4. `init` Detection Engine (`internal/config/detect.go`, M14)
`Scaffold` returns a `ScaffoldResult` (the `*models.Config` plus the source of each detected value) so `init` can explain itself. Detection is **local and side-effect free**: it reads files under the project directory and never opens an SSH connection, a Docker socket or a network.

- [x] **Port precedence, in three evidence tiers (M14).** Not one flat list – the tiers exist because the sources are not equally trustworthy, and the driver also changes what the right answer *is*:
  - **Tier 1, the image (ground truth).** A Dockerfile or compose file describes the container that will actually run, so it outranks every piece of framework knowledge.
    - `driver.type == "compose"` → the compose mapping outranks `EXPOSE`, because `ports: ["8080:80"]` means host `8080` and that is what ingress needs.
    - last `EXPOSE` (the final stage becomes the image) → **final stage is a plain web server** (`nginx`/`httpd`/`apache`/`caddy` without `EXPOSE` ⇒ 80; a Vite/React/Vue static build ships in a `nginx:alpine` stage far more often than in a Node runtime, where the framework's dev port would be wrong) → **a port in `CMD`/`ENTRYPOINT`/`ENV`** (`--port 4000`, `PORT=8080`): `CMD ["next","start","-p","4000"]` pins 4000 and no `next`→3000 knowledge overrides it.
  - **Tier 2, a literal in the project (a fact).** An explicit `--port N`/`-p N` in an npm script, then the bounded source scan. It sits *above* the conventions: a port written in the app's own code is a fact, a framework default is a guess.
  - **Tier 3, conventions (weakest).** The `package.json` framework table, then the Python dependency table (servers first: a container runs `uvicorn`/`gunicorn`, so a `flask`+`gunicorn` project resolves to 8000, not Flask's 5000 dev default).
  - **Nothing detected → no value at all.** No `8080` fallback: `app.port` is left unset (OD-04) and the deploy refuses the config with an actionable message.
- [x] **Compose file** (`findComposeFile`): the first of `docker-compose.yml`, `docker-compose.yaml`, `compose.yml`, `compose.yaml`. Fixes a latent M7 bug: a project with `compose.yml` was detected as `compose` but still got `compose_file = "docker-compose.yml"`, i.e. the driver ran a file that does not exist. Parsed with `gopkg.in/yaml.v3` (only `services.<name>.ports` / `.expose` are modeled); `ports` accepts the short (`8080:80`, `127.0.0.1:8080:80`, `8080/udp`) and long (`target`/`published`) forms, and returns the **host** side; service choice prefers `web, app, api, frontend, server, …` then alphabetical, so the result is deterministic. `expose:` / `target` is the fallback.
- [x] **`package.json`**: an explicit port in a `scripts` entry wins (`next dev -p 3000`, `vite --port=5173`, `PORT=…`; `tsc -p tsconfig.json` cannot match), then a `vite preview` in the `start` script (4173 – the production container, not the 5173 dev server), otherwise the framework's conventional port from `dependencies`/`devDependencies` (`next`/`nuxt`/`@nestjs/core`/`@sveltejs/kit`/`react-scripts` 3000, `vite` 5173, `@vue/cli-service` 8080, `@angular/cli` 4200, `astro` 4321, `gatsby` 9000, `serve` 3000, `http-server` 8080, `express` 3000). The map is deliberately small and mainstream: a hit that is wrong costs a fallback, not a wrong deploy.
- [x] **Bounded source scan (limits locked by test, M17)** – the last resort, and the reason it exists: **a manifest cannot declare a Go port.** `go.mod` has no port field and never will, because the language deliberately has no manifest-level default; the port lives in `main.go`. Patterns, in order: `ListenAndServe(":N")`, `Addr: ":N"`, `PORT=N`, an explicit `--port N`/`-p N` flag (shell *and* JSON-array form, so a `Dockerfile` `CMD ["uvicorn", …, "--port", "8000"]` counts; the separator gap is bounded and the digit group required, so `tsc -p tsconfig.json` and `--port-from-file` cannot match), a Python `port=N` keyword (`uvicorn.run(app, port=8000)`), `.listen(N)`, Django `runserver 0.0.0.0:N`, gunicorn `-b 0.0.0.0:N`, Spring `server.port=N`. Walk limits: ≤400 files, ≤4 MB read, skip `.git`/`node_modules`/`vendor`/`dist`/`build`/`target`/… , only known extensions plus extension-less `Procfile`/`Makefile`/`Dockerfile` and `.env*`. Version-like numbers (`20240101`, timeouts) and prefixed variables (`DB_PORT`, `serial_port`) cannot match – the underscore blocks the word boundary. **M17:** the walk limits are a performance contract, not an implementation detail – a regression that dropped the caps would fail no port-detection test, it would just make `init_project`, an MCP tool an agent calls on a repository it has never seen, walk an unbounded tree. `TestSourceScanRespectsWalkLimits` builds a project with more files than the cap and ports hidden inside skipped directories, and asserts neither contributes; the counterpart asserts a real port is still found under the cap. A user-supplied ignore file is deliberately **not** honored (the walk skips by directory name only): `init` reads no `easydrop.toml` and opens no project config, so a `.easydropignore` cannot silently change what `init_project` sees. `TestSourceScanHonorsDockerignoreStyleExclusions` exists to keep that property from being "fixed" by accident.
- [x] **Stack** (`detectStack`): `go.mod` → go, `package.json` → node, `pyproject.toml`/`requirements.txt`/`manage.py` → python, `Cargo.toml` → rust, `Gemfile` → ruby, `composer.json` → php. Reported for the user's benefit only – no config value is derived from it.
- [x] **App name**: the directory name, unless it is a generic placeholder (`src`, `app`, `project`, `test`, …), in which case the Go module path's last element (major-version suffix stripped: `github.com/acme/api/v2` → `api`) or the npm `name` is used. A descriptive directory name always wins.
- [x] **Python conventions** (`detectPythonPort`, `pythonDependencies`): the Node equivalent of the `package.json` table, since a requirements file is the only place a Python project names its server. Dependencies are collected from `requirements*.txt`, `pyproject.toml` (`[project] dependencies`), `Pipfile` and `setup.py` (`install_requires`) – extras, version specifiers and environment markers are stripped (`uvicorn[standard]>=0.29` → `uvicorn`). Servers first: `uvicorn`/`gunicorn`/`hypercorn`/`daphne` 8000, `waitress` 8080, `fastapi` 8000, `flask` 5000, `django` 8000, `sanic` 8000, `aiohttp` 8080, `pyramid` 6543, `bottle` 8080, `tornado` 8888, `streamlit` 8501, `gradio` 7860.
- [x] **Explicit override (M15):** `ScaffoldOptions{Port}` / `init --port N` / `init_project {"port": N}` sets the port verbatim and outranks every detection source, reported as `explicit override`; `0` or omitted means "detect". Validated 1-65535 in `ScaffoldWith`, so both interfaces reject the same values. This is the *supported* exit from the "no port" state, which is what makes omitting the key defensible.
- [x] **One report (M15):** `ScaffoldResult.Report()` (`internal/config/report.go`) is the single wording of an `init` outcome; the CLI prints it and `init_project` returns it verbatim. It previously existed twice and drifted – the CLI listed where to look for a port, MCP did not – and an agent reading a weaker variant than the human is the exact failure mode worth avoiding. The port-missing block therefore gives the **method** (where the server binds), states that easydrop has no tool to set the port so the agent must edit the file, and names both escape hatches; asking the user is limited to the genuinely ambiguous case.
- [x] **Honesty (locked):** when nothing is detected, **no port is written** – not even a default (OD-04). `init` prints `port NOT SET` and an `ACTION REQUIRED` block; the generated config is deliberately incomplete and `ApplyDefaults` refuses it, naming the field and both failure modes (healthcheck timeout for `single`, 502 proxy for compose/swarm). `ApplyDefaults` is split so that `init` can run every non-port default while leaving `port`/`host_port` unset. Malformed input (broken JSON/YAML, `EXPOSE banana`) degrades to the next source and never fails `init`.

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
- [x] Auth method assembly (`authMethods`, M17) – order locked: explicit `server.ssh_key` → `ssh-agent` → `server.password`. An `SSH_AUTH_SOCK` that is **set but unreachable** is recorded and named in the "no usable method" error rather than dropped in silence, because that is what a spawned MCP server started by an AI client looks like: the variable is inherited (or absent) while the agent behind it is not reachable from the child, and the bare `no ssh auth methods` sends the user looking in the wrong place. A dead socket never blocks the key-file or password methods – it only sharpens the diagnostic when nothing else is configured. See `docs/mcp-spec.md` §0.1 for the client-side contract.

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
- [x] Init local probing loops: execute networking diagnostics inside the host context hitting `[http|https]://localhost:[temp_port][health_check_path]` (path from `app.Config.App.HealthCheckPath`, default `"/"`) every 2 seconds (up to 10 total validation retries) via `curl -fsS -o /dev/null -w '%{http_code}'`, expecting `200`. Any error/non-200 is a miss. `ctx` cancellation aborts the loop.
- [x] **Scheme-aware probing (M20) – `app.health_check_scheme`.** The probe above could only speak plain HTTP, so an app that answers *only* over TLS, or that redirects everything to HTTPS, could never be deployed: every attempt missed and the deploy failed with a healthcheck timeout for an app that was perfectly healthy. That shape is not exotic – `return 301 https://$host$request_uri` is what most nginx front-ends do, so easydrop could not deploy a large part of what it exists to deploy.
  - **`""`/`auto` (default, unchanged for existing configs):** try HTTP, and on a miss try HTTPS **within the same attempt**. HTTP is retried on each later attempt, so a container that gains a plain listener is picked up again rather than being permanently pinned to TLS.
  - **`http` / `https`:** pin the probe. An explicit `https` that fails does *not* fall back – the user stated the scheme, and silently probing something else would defeat the setting.
  - **`-k` on the HTTPS probe, deliberately.** The probe connects to `localhost`, so a certificate issued for the domain would fail verification even when perfectly valid. What this probe tests is whether the app *answers*, not whether it holds a trusted certificate: nginx terminates the user's TLS in front of it.
  - **Redirects are not followed (`curl -L` is deliberately absent).** `-L` would also "fix" an app whose health path redirects to some unrelated 200 page. That is a false pass, and a silent one. A redirect is treated as a miss, which is what triggers the HTTPS fallback.
  - Rejected at parse time rather than defaulted: `app.health_check_scheme = "tls"` is an error, because silently probing HTTP for an app the user declared as TLS is exactly the timeout the setting exists to prevent. `"auto"` is accepted as an explicit spelling of the default and normalized to `""`, so one internal representation carries the meaning.
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
- [x] **Host port pre-flight (`ensurePortFree`, M17).** Before *every* `docker run` – Blue-Green's green stage and the direct-mode container alike – check that the target host port is free, and fail with the port, the squatter and the remedy. In Blue-Green this is the difference between a diagnosable failure and a mystery: the alternation above knows only which port *this app* occupies, so a stranger on `host_port+1` otherwise surfaces as Docker's `port is already allocated` – after the image has been built and shipped, naming neither the port nor the culprit, and looking like an easydrop bug.
  - **Probe, not bind.** `ss -ltnpH` with a `netstat -ltnp` fallback, matching the local address by `:port` suffix rather than a field index (the two tools lay out fields differently and neither is worth parsing fully). Binding a test socket was rejected: it would need a writable temp path on a possibly bare host, and it cannot tell a foreign listener from the container we are about to replace.
  - **Best-effort, never blocking.** No listing tool, or a probe that errors, logs `port N pre-flight skipped` and proceeds – a deploy must not fail on a missing diagnostic utility, and Docker remains the authority.
  - **Confirmed, not instantaneous.** A "busy" verdict is re-checked up to `portCheckAttempts` (3) times, `portCheckRetryDelay` (500ms) apart, before it is believed: on a redeploy the port belonged to the container just removed, and docker's userland proxy can hold the socket a moment longer. Without the retry every redeploy would be flaky.
  - **Ordering is load-bearing.** Direct mode checks *after* its `docker rm -f [appName]-active` – that removal is what frees the port, so checking first would fail every redeploy of an existing app.
  - Unattributable sockets (root-owned `docker-proxy` seen from an unprivileged easydrop) are reported as occupied with the owner explicitly unnamed, rather than guessed at.

### 5.2. Status, Logs, Teardown (`internal/core/drivers/single.go`, same file)
- [x] `Status(ctx, appName) (*models.AppStatus, error)`: `docker ps -a --filter 'name=^/[app]-active$' --format "{{.State}}|{{.RunningFor}}"` → running→`Up`, restarting→`Restarting`, anything else/missing→`Down` (uptime from RunningFor). `SSLStatus` stays empty – enriched by the ingress layer (M6).
- [x] `Logs(ctx, appName, lines, follow) (<-chan string, error)`: `docker logs --tail [lines] [app]-active` split into a buffered channel. `follow=false` closes after the snapshot; `follow=true` polls every `FollowInterval`, emitting only unseen lines (dedup window of last 500) until ctx cancellation – polling (not blocking `docker logs -f`) keeps it ctx-aware on both Local and SSH executors.
- [x] `Teardown(ctx, appName) error`: `docker rm -f` active + leftover green + rollback backup (`[app]-active-previous`); missing containers are skipped – teardown is idempotent. **Driver level only:** removing the nginx vhost is not the driver's business, so it happens one layer up in `deploy.Teardown` (§5.4) and only after the driver has succeeded.
- [x] **`Status` no longer leaves `SSLStatus` empty (M19).** The field was declared, plumbed through the CLI and `get_status`, and never filled by anything – so the one question a user asks after an HTTPS deploy ("is it actually serving TLS?") had no answer. It is now filled by `deploy.tlsStatus` from the **running** nginx configuration (`nginx -T`), not from `easydrop.toml`. The distinction is the point: config says what was asked for, `nginx -T` says what is loaded, and they diverge exactly when certbot rewrote the vhost, when something else removed it, or when the certificate could not be issued. For self-signed targets the file is read for the expiry date and flagged `EXPIRING` inside the renewal window. Only for a `Up` app – TLS on a downed container is history, and reporting it would suggest the deployment is healthy.

### 5.3. Rollback (`Rollback(ctx, app) error`, same file + interface)
- [x] Contract (locked, part of `DeploymentDriver`): restores the stopped backup kept by the last Blue-Green deploy. Flow: `docker inspect [app]-active-previous` (missing → fail fast with "no rollback backup", zero mutations) → `docker rm -f [app]-active` → `docker rename [app]-active-previous [app]-active` → `docker start [app]-active` → resolve port via inspect (fallback `app.Port`) → ingress update → probe. Consumes the backup: a second rollback reports "no backup" until the next Blue-Green deploy. Probe failure leaves the restored container running (last resort stays up) and returns an error. Direct-mode deploys keep no backup → `Rollback` explains that.

### 5.4. Teardown Orchestration (`internal/deploy/deploy.go`, Milestone 19)

`deploy.Teardown(ctx, configPath, appName, TeardownOptions{Purge}) (*TeardownResult, error)` is the shared entry point behind both `easydrop teardown` and `teardown_app`. It runs the driver teardown, then the ingress teardown, then the optional certificate purge.

- [x] **The nginx vhost is removed by default, not on request.** Leaving it behind is not a neutral state: nginx keeps proxying to a port with no container on it, so the domain answers `502` instead of refusing the connection, the deployment looks half-alive, and nothing in the output says why. easydrop overwrote `sites-available/<domain>` and its `sites-enabled` symlink on every deploy, so it owns exactly those two files and nothing else under `/etc/nginx`.
- [x] **Order is load-bearing: containers first, ingress second.** If the container removal fails we never reach the ingress step and the host is left exactly as it was. The reverse order would remove the vhost and then fail to stop the container – the app keeps running with nothing in front of it.
- [x] **Reload is guarded, and a failed one is reported.** `RemoveIngress` deletes the files, runs `sudo nginx -t`, and reloads only if the test passes. Reloading a config nginx rejected takes down every other site on the host. When the test fails the running nginx still serves the config it had already loaded, so the result carries `IngressReloaded: false` plus a note saying the domain will answer 502 until someone reloads – verified against a real nginx, not assumed.
- [x] **`--purge` / `purge` is opt-in and self-signed-only.** A Let's Encrypt certificate is never deleted whatever the flag says: certbot owns it, has its own renewal timer, and may still be serving the domain through a hand-written vhost. The self-signed pair is easydrop's own and is re-created in milliseconds by the next deploy – but re-trusting it in a browser costs the user a click, and the same domain may be served by a vhost easydrop never wrote. Asking is the right default.
- [x] **`TeardownResult` instead of a bare error.** `IngressRemoved` / `IngressReloaded` / `CertPurged` / `IngressNote` let each interface state what actually happened – including the idempotent no-op and the "no domain configured" case – rather than a single "done" that would be a lie in three of those situations.
- [x] **Not touched, deliberately:** named volumes (data outlives the deploy), the built image (`build.image` may be shared with another app, and easydrop cannot know who else it feeds), the Let's Encrypt certificate, and `manage_server` records.

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
- [x] **Ingress removal (`RemoveIngress`, M19).** Deletes `sites-enabled/<domain>` **then** `sites-available/<domain>` – the symlink first, because that is the file nginx actually reads, so removing the real file first would leave a dangling link and break `nginx -t` for a reason unrelated to the user's config. `rm -f` makes it idempotent; existence is probed first so a no-op teardown neither deletes nor reloads. Returns `IngressRemoval{Removed, Reloaded, ReloadReason}` rather than an error when `nginx -t` fails, because the files are already gone and the caller must be able to say so honestly: the running nginx still serves the old vhost, so the domain keeps answering 502 until it is reloaded. Verified against a real nginx: with the vhost deleted but no reload the site still answers 200, and after the reload it refuses the connection – the note describes observed behaviour, not a guess.
- [x] **TLS status (`TLSStatus`, M19).** Reads `sudo nginx -T` – the **running** configuration – extracts the block belonging to *this* domain by its `# configuration file /etc/nginx/sites-available/<domain>:` header, and reports whether that block terminates TLS on 443. Reading nginx instead of `easydrop.toml` is the whole point: the two diverge exactly when certbot has rewritten the vhost, when something else removed it, or when a certificate could not be issued, and reporting the configured intent would be a claim easydrop cannot back. Another site's `listen 443 ssl` must not be attributed to this domain, so the header match is exact. An unreadable `nginx -T` (no nginx, no sudo) yields "" – "no answer", never "no TLS".
- [x] **The served certificate is the source of every fact (M20).** The block names the file (`ssl_certificate …`); easydrop reads it and parses it with the same `crypto/x509` the generator uses. `nginx -T` already contained the path, so the Let's Encrypt path had no excuse for reporting only "TLS" with no date – the asymmetry M19 introduced is gone, and both modes now report `expires YYYY-MM-DD`, an `EXPIRING` flag inside the renewal window, and coverage.
  - **Coverage check (`DOES NOT COVER <domain>`).** A certificate that does not name the domain is the failure that looks like nothing at all: nginx starts happily, the deploy healthchecks green, and only the client complains. Verifying the SANs is the only place that can be caught before it reaches a user.
  - **Kind derived from the certificate, not from config.** `issuer == subject` means self-signed; anything else is reported as `TLS`. Reading `nginx.self_signed` here would re-introduce exactly the config-vs-reality gap M19 was built to close – a domain re-issued by certbot, or a config switched after the last deploy, would be mislabelled.
  - A `fullchain.pem` holds several PEM blocks; the first is the leaf, and the leaf's expiry is the one a client actually notices.
- [x] `SelfSignedCertifier.Describe` was removed in M20 as superseded: it read a *path easydrop chose*, while `TLSStatus` reads the path *nginx* names. Two sources for one fact is how they drift.

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
- [x] Intercept local environment rules: if configuration metrics address `"localhost"` or `"127.0.0.1"`, exit early with a success code (Let's Encrypt does not offer domain validation flows across local network endpoints). **Superseded for self-signed targets in M18** – `nginx.self_signed` generates the certificate locally and `deploy.Run` no longer calls Certbot at all in that mode, so this early return is now only reachable for a config that asks for Let's Encrypt on localhost, which is a user error the validation below rejects.
- [x] If `email` is empty, provision with `--register-unsafely-without-email` (locked decision):
  ```bash
  sudo certbot --nginx -d [domain] --non-interactive --agree-tos --register-unsafely-without-email
  ```
  Otherwise:
  ```bash
  sudo certbot --nginx -d [domain] --non-interactive --agree-tos --email [email]
  ```
- [x] **Why EasyDrop writes no TLS block here (locked).** The managed vhost is HTTP-only; certbot adds `listen 443 ssl` itself once ACME succeeds. That is not an omission – ACME HTTP-01 needs a live vhost on :80 for the domain, so the 443 block cannot exist before the certificate does. §6.2A is the mode where the ordering is reversed and easydrop *does* own the block.
- [x] Config validation gating the mode choice (`validateNginx` in `internal/config`, M18): `self_signed` requires `ssl` (it selects *how* the certificate is obtained, not whether TLS is served); `ssl` requires a non-empty `domain` (nothing to certify otherwise); a self-signed `domain` must be one a certificate can name. This is what turns the old silent localhost no-op into an actionable error.

### 6.2A. Self-Signed Certificate Provisioner (`internal/core/infra/selfsigned.go`, Milestone 18)

For `nginx.self_signed = true`: easydrop generates the leaf itself instead of asking an ACME authority for one. The driver is the same one (`single`/`compose`/`swarm` all reach it) and the config change is one boolean.

- [x] `SelfSignedCertifier.EnsureCert(ctx, domain) (CertResult, error)` – idempotent: provisions only when the certificate is missing, unreadable, expiring within `renewBefore` (30 days), or naming a different host. **Reuse is the feature.** A certificate regenerated on every deploy changes its fingerprint, so the warning the user already learned to accept would come back on every single deploy; with reuse, a normal deploy never touches it. An unreadable certificate file regenerates rather than failing the deploy – nginx could not start with it anyway, so refusing would replace a broken site with no site.
- [x] **Pure Go, no `openssl` on the host.** `crypto/x509` + `ecdsa` P-256/SHA-256. Shelling out would add a host dependency for something the binary does in a millisecond, and on a remote target that dependency would have to be installed first. P-256 rather than Ed25519: Ed25519 is newer and shorter, but older JDKs, some Go/Java HTTP clients and embedded stacks will not negotiate it. Private key generated in-process and written straight to the host – it never touches disk here.
- [x] **SAN, not CN.** Modern browsers and Go's `crypto/tls` ignore the Common Name and read only SubjectAltName; a certificate with `CN=localhost` and no SAN is rejected by everything current. `setSANs` emits `DNS:<domain>` or `IP:<ip>` accordingly, plus `127.0.0.1` and `::1` for loopback names (RFC 6761 reserves `*.localhost`, so the claim is a fact). A private name from `/etc/hosts` gets **no** IP SAN – easydrop does not resolve DNS and must not assert where a name points. CN is still set, for legacy clients only. Backdated `NotBefore` by an hour so a host clock a minute behind does not reject it.
- [x] Paths and permissions: `/etc/nginx/ssl/<domain>.crt` (0644) and `.key` (**0600**). Installed through the usual privileged-path discipline – staged under `core.StagingBase()`, then `sudo mv` + `sudo chmod`, because SFTP cannot write under `/etc/nginx`.
- [x] **Trust is reported, never installed.** easydrop does not put the certificate into the system trust store: that needs sudo on the developer's machine and silently changes what every other program there trusts – mkcert's decision to make interactively and explicitly. The message prints the exact `update-ca-certificates` command and a `curl --cacert` alternative instead. On reuse it says so, because "your trust decision still holds" is the reassuring half of the feature.
- [x] **`RemoveCert` (M19)** – purge for the self-signed pair only, behind an explicit flag, and it refuses a Let's Encrypt path on principle: certbot owns that certificate, renews it on its own timer, and may still be serving the domain through a vhost easydrop never wrote. Both files are deleted or neither is – a half-removed pair leaves nginx unable to start on the next reload, which is worse than leaving both. Only the two easydrop-created paths are touched, so a `/etc/nginx/ssl` shared with other tools survives.
- [x] Verified: unit coverage of the shape, SANs, validity, reuse/rotation decisions and the command sequence; `TestGeneratedCertVerifiesInGoTLS` loads the pair with `tls.X509KeyPair` and verifies it for `localhost`, `127.0.0.1` and `::1` through a real x509 verifier; `TestNginxTLSTemplateIsAcceptedByNginx` renders the template with a real generated certificate and runs the real `nginx -t` on it (skipped when nginx is absent, only the listen ports are rewritten because `-t` binds them); and a live smoke against a running nginx served easydrop's own certificate over both ports, with `curl --cacert` succeeding and plain `curl` refusing (exit 60) as intended.

### 6.3. Reverse proxy TLS mode (`templates/nginx-tls.conf.tmpl`, Milestone 18)

- [x] A **separate** template, not a conditional inside `nginx.conf.tmpl`. The plain vhost must never reference a certificate – certbot owns that block – and that invariant is best protected by two files: a shared template with a branch is exactly how an innocuous edit eventually breaks it. `TestNginxPlainTemplateNeverReferencesCertificate` pins the invariant.
- [x] Ordering is load-bearing: `NginxManager.Apply` provisions the certificate **before** rendering and testing the config. `nginx -t` validates that `ssl_certificate` exists, so provisioning afterwards would break the first TLS deploy on a host with no previous vhost to fall back to. Asserted by `TestNginxTLSProvisionsCertificateBeforeConfigTest`.
- [x] **Port 80 keeps proxying instead of redirecting (locked).** In self-signed mode a `301` would turn a working `http://` URL into a certificate warning page – strictly worse for the local development this mode exists for. The Let's Encrypt path is the opposite: certbot installs the redirect itself. `ssl_protocols TLSv1.2 TLSv1.3` only; the private key path is never logged.
- [x] Scope kept narrow: the certificate lives on the host where nginx runs (nothing else consumes it), `teardown` leaves it alone (consistent with leaving the vhost in `sites-available`, §5.2), and `nginx.email` is ignored in this mode.

---

## §7. Single-Binary CLI (`cmd/easydrop`, Milestone 7)

- [x] Single binary entry point `cmd/easydrop/main.go` (locked – no `cmd/cli` + `cmd/mcp-server` split; NFR-03). Command tree lives in `internal/cli/` (`root.go` + one file per command); `main.go` only calls `cli.Execute()`.
- [x] CLI framework `github.com/spf13/cobra` WITHOUT `viper` (locked – single `easydrop.toml`, cobra flags suffice).
- [x] Commands from `docs/cli-spec.md`: `init [--force]` (via `config.Scaffold`+`WriteConfig`), `deploy [-c/--config] [--no-cache] [--blue-green] [--skip-bootstrap]`, `status`, `logs [app_name] [-f/--follow] [-n/--tail]`, `rollback [app_name]`, `teardown [app_name]` (M12). `mcp-server` lands in Milestone 8.
- [x] `deploy` pipeline (`runDeploy`): parse → overlay `--no-cache`/`--blue-green` (`applyDeployFlags`, never unsets config-true) → gate `driver.type == single` → signal-aware ctx → `NewExecutor` → Bootstrap (skipped with `--skip-bootstrap`) → `Build` with `SrcDir` = config file's directory → `NewDriver` (`BlueGreen` from config; `Ingress` wired when domain non-empty, built as the TLS manager when `nginx.self_signed` – M18) → `Deploy` → Certbot when `nginx.ssl && !nginx.self_signed && domain != ""` (the self-signed mode is already complete by the time `Deploy` returns).
- [x] `init` scaffolding (`config.Scaffold`, FR-02, **M14 detection rules in §1.4**): `WriteConfig` refuses overwrite without `--force`; output round-trips through `ParseConfig` (tested). Every detected value is reported with its source (`ScaffoldResult`), and a defaulted port is called out in the output.
- [x] Cross-field note (locked): `Bootstrapper.ensureDockerGroup` checks `id -nG` membership first and skips `usermod` when already in the docker group (idempotent, avoids pointless sudo).
- [x] `logs` filtering (M16): `-g, --grep` takes a case-insensitive regexp. **The filter lives in the core, not the CLI** (`deploy.Logs(ctx, appName, LogOptions)` + `collectLogs`), because a second implementation would drift from `get_logs` and the two interfaces are required to agree. `--tail` is documented as lines *scanned*, not lines returned. An invalid expression exits non-zero; an empty read explains itself on stderr, because "no logs" and "no matches" are different facts.

## §8. MCP Server (Milestone 8)

- [x] Serve stdio JSON-RPC via `easydrop mcp-server` using the official `modelcontextprotocol/go-sdk` v1.8.0 (locked). `internal/mcp/server.go`: `NewServer()` + `Run(ctx)` over `mcp.StdioTransport{}`; `internal/cli/mcpserver.go` wires the cobra subcommand with signal-aware ctx. Single `internal/version.Version` (`1.0.0`, ldflags-overridable) feeds both `--version` and MCP `serverInfo`.
- [x] One core, two interfaces (locked): the deploy pipeline lives in `internal/deploy/` (`Options`, `Run`, `LoadConfig`, `ResolveAppName`, `ApplyFlags`, `Status`, `Logs` with collection cap, `Rollback`, `Init`); `internal/cli` commands are thin wrappers, MCP handlers call the same functions. No CLI↔MCP imports.
- [x] Tools map 1:1 to `docs/mcp-spec.md`: `init_project`, `deploy_app`, `get_status`, `get_logs`, `rollback_app`, `teardown_app` (M12), `manage_server`; resources `easydrop://docs/schema` (**generated** by reflection over `models.Config` – `schemaMeta` supplies descriptions/defaults/enums, missing entry = error, so schema can never drift from the parser) and `easydrop://docs/troubleshooting` (static runbook). Typed I/O with `jsonschema` tags; app failures return `isError` tool results (not protocol errors); `follow` log collection capped at 2000 lines.
- [x] `manage_server` persistence: **encrypted** vault `~/.easydrop/servers.vault` – AES-256-GCM, scrypt `(N=32768, r=8, p=1)`, dir `0700`, file `0600`, upsert by host+user. Key material comes only from `EASYDROP_VAULT_PASSWORD` (no prompt – MCP is non-interactive; fail closed). Legacy plaintext `servers.toml` is imported once and renamed `servers.toml.migrated`. `EASYDROP_SERVERS_FILE` overrides the path. Secrets never echoed in messages or logs (tested).
- [x] Verified: unit tests (store round-trip/perms/validation, handler arg validation, schema registration) + in-process e2e with a real SDK client (`e2e_test.go`: tools/list, init→EXPOSE detect, manage add + secret-leak check, resources, status-without-config isError) + live stdio smoke against the built binary (initialize, tools/list, init_project, manage_server, get_status error path, resources/list+read).
- [x] **Tool annotations (M16).** All seven tools declare `ToolAnnotations` explicitly via `tool()`/`readOnly()`/`mutating()` in `server.go`. Not cosmetic: the protocol defaults are `readOnly=false, destructive=true`, so an unset annotation describes `get_status` and `get_logs` as mutating – precisely the signal a client uses to decide whether it may answer a question unattended. `destructiveHint` is omitted on the read-only tools (the spec scopes it to `readOnlyHint == false`), and `rollback_app` is marked **non**-idempotent because it consumes the single backup. Every destructive tool's description states that the agent must confirm with the user: the hint alone does not stop a model, and the description is what it reads. Per spec these hints are not an authorization boundary. Table in `docs/mcp-spec.md` §1.0; tested over a real session (`TestE2EToolAnnotations`, `TestE2EDestructiveToolsAskForConfirmation`).
- [x] **Log filtering + ambiguity-free empty results (M16).** `get_logs` takes `filter` (case-insensitive regexp, applied in the shared core) and returns `scanned`/`matched` next to `lines`. Three rules make it usable by an agent rather than merely smaller: a filtered read with no explicit `tail` scans 1000 lines instead of 50 (a narrow window returns "0 matched", which reads as "no errors"); an invalid expression is an `isError` result, never a silently-passing filter; and an empty read returns an explanatory line distinguishing "container logged nothing" from "nothing matched". The text payload is bounded at 400 matched lines with an explicit truncation marker. See §7 for the shared core.

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

### OD-03: How do credentials reach the SSH layer without living in git?
- **Context:** `server.password` was the only way to authenticate without a key
  file, and it lives in `easydrop.toml` – i.e. in git. The encrypted vault (D-06)
  did not help: `manage_server` is reachable only from the MCP tool and nothing
  in `internal/deploy` ever read it, so the stored credential was inert.
  `.env` interpolation was the conventional answer (docker compose, systemd).
- **Options:**
  - **A:** wire the vault into deploy. The vault lives in `internal/mcp`, which
    imports `internal/deploy` – this needs a package extraction to avoid an
    import cycle, and it makes the CLI depend on `EASYDROP_VAULT_PASSWORD`
    (non-interactive, fails closed). Biggest change, smallest reach: the vault
    is still MCP-only, so a plain CLI user gains nothing.
  - **B (chosen):** `${VAR}` interpolation in the config + optional
    `.easydrop.env`/`.env`. Works identically for the CLI and for MCP (both go
    through `ParseConfig`), needs no new dependency, and the secret file is
    something every repo already knows how to git-ignore.
  - **C:** document "use ssh-agent" only. Zero code, but leaves password auth
    users with no in-repo answer.
- **Decision (M13, option B):** see §1.2A for the locked semantics. The
  whitelist is explicit so a new field is never interpolated by accident, and
  errors name the field and the variable but never the value (NFR-02). Since
  easydrop now reads those files itself, `.env` and `.easydrop.env` were added
  to `defaultExcludes` in the archiver: a deploy must not ship the credential it
  authenticates with. Projects that need `.env` inside the bundle (compose
  variable substitution) re-include it via `!.env` in `.dockerignore` – a
  behavior change worth calling out in release notes.
- **Status:** RESOLVED in M13. Option A is not dead, just separate: it would
  only matter if the vault ever becomes a deploy-time credential source.
- **Verified:** unit tests for every documented case plus a manual check that an
  unset variable aborts the command before any SSH connection is attempted.

### OD-04: Should `init` default the port when detection finds nothing?
- **Context (M14 smoke):** with no `EXPOSE`, no compose file and no port literal
  anywhere, `init` wrote `app.port = 8080`. The failure mode of a wrong port is
  quiet and expensive: the image builds, the container starts, and the problem
  only appears later – for the `single` driver as a healthcheck timeout after 10
  attempts, and for **compose/swarm as a fully successful deploy with a 502
  proxy**, because those drivers have no HTTP probe at all
  (`ComposeDriver.checkRunning` only inspects `compose ps`). The user would
  then debug "the service is unreachable" with an invented number in the config
  as the cause.
- **Options:**
  - **A:** keep the 8080 default and warn in the output. Cheap, but the config
    on disk still contains a value nobody chose – and anything that reads the
    file without the warning (an agent, a CI script, a teammate) inherits it.
  - **B (chosen):** write no port. `app.port` gets `omitempty`, so the key is
    absent from the generated file; `ApplyDefaults` then refuses it with a
    message naming the field and explaining both failure modes. The file is
    deliberately incomplete, one edit away from working, and every consumer
    fails fast instead of deploying a guess.
  - **C:** refuse to write the file at all. Worst of both: the user gets nothing
    to edit, and `init` stops doing its job.
- **Decision (M14, option B; escape hatch added in M15):** `Scaffold` skips the port pair entirely when
  detection is empty (it calls `applyNonPortDefaults`, so `host_port` is not
  fabricated from an absent `port` either). `init` prints `port NOT SET` plus an
  `ACTION REQUIRED` block listing the usual suspects; `init_project` tells the
  agent to ask the user and set the port *before* deploying, so an agent cannot
  paper over the gap. Every other default is still written.
- **Side effect worth knowing:** requiredness in the generated JSON Schema used
  to be inferred from the `omitempty` tag, which would have dropped `app.port`
  from `required` and made the published schema contradict the parser. It is now
  an explicit list (`requiredFields` in `internal/mcp/schema.go`).
- **Status:** RESOLVED in M14.
- **Verified:** `TestScaffoldOmitsPortWhenUndetected` asserts the written file
  contains neither `port` nor `host_port`, still carries every other default,
  and that `ParseConfig` rejects it with the actionable message; a live run
  confirms `easydrop deploy` fails on such a config before opening any
  connection to a host.

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

---

## §10A. Cross-platform CI and the portability contract

`.github/workflows/ci.yml` has two distinct kinds of job, and conflating them is the
usual way a matrix becomes decorative:

- **`build` (6 triples, one Linux runner).** Cross-compilation with `CGO_ENABLED=0`.
  It proves the code *compiles* for each target. It never *runs* it.
- **`test-native` (ubuntu / macos / windows).** Proves the suite *runs* on each
  host OS. A cross-compiled binary is never executed by this repo's CI, so without
  these jobs a platform whose tests behave differently would ship green.

The native jobs earned their place on their first run: they failed, and the failures
were real rather than environmental. `go vet` for `GOOS=darwin|windows` had already
passed locally, which proved the *types* compile and nothing more – a distinction
worth remembering, because type-checking a platform is exactly the check that is
misread as "it works there".

### The artifact that was named after a variable

The first tagged run uploaded an artifact literally called
`easydrop-${GITHUB_REF_NAME}` – 30 MB of correct binaries under a name that meant
nothing. The cause is a distinction that is invisible in the YAML: **an action
input is not a shell.** `run:` executes in a shell, so `${GITHUB_REF_NAME#v}`
expands there; `name:`, `path:` and friends are plain strings passed to the
action, where `$VAR` is literal text and only `${{ expr }}` is expanded. The
`${{ }}` form sitting a few lines above in the same file made the shell form look
correct by analogy.

`make check-workflow` now fails `verify` on that class of mistake: any bare
`$VAR` / `${VAR}` in a YAML scalar that is not a `run:` or `env:` block. It is a
grep, not a YAML parser, and the escape through `make` is itself worth recording –
the first version of the pattern lost its `\$` to make's own expansion and flagged
every line containing an uppercase word (`name: CI`, `'refs/tags/v'`). The
negative cases are checked too: a legitimate `${{ matrix.os }}` in an action input
and a legitimate `$GITHUB_REF_NAME` inside `run:` must both stay quiet, or the
guard is worse than nothing because people learn to ignore it.

### Publishing the release, and why the step is idempotent

The `release` job is the only one with `contents: write`; everything else stays
read-only. It runs `check-version`, builds, and then publishes.

Publication was originally left to a human, on the theory that a broken
auto-publish is worse than a manual step. That reasoning was right while the
pipeline was unproven and wrong once it worked: a manual upload is seven files
with platform names in them, and the realistic failure is shipping five of six
binaries, or none of the checksums. The publish step is `gh release create`,
which is a thin wrapper over an API call that either works or reports why.

It is **idempotent by design**: if the release already exists the step uploads
with `--clobber` instead of failing. That is not defensive decoration – a re-run
is the normal case when a flaky test is retried or a tag is re-pushed onto a
rebuilt commit, and without it the pipeline would be stuck on "release already
exists" with nothing left to do. Re-running produces a release with the same tag
and freshly built assets, which is the honest state of that tag.

### Release hygiene: `check-version`

A tag is the only thing that triggers the `release` job, and it is also the one
input nobody re-reads before shipping. Two lists can disagree with the tag: the
`var Version` the binary stamps into `--version` and the MCP `serverInfo`, and the
`## [x.y.z]` heading in `CHANGELOG.md`. When they do, the failure is invisible —
`v1.1.0` ships binaries that report `1.0.0`, and nobody notices until a user files
a bug quoting a version that does not exist.

`make check-version EXPECTED=<tag>` closes that, and it runs as the **first** step
of `release` so the failure names the mismatch instead of producing an artifact
nobody will re-check. It reads the version out of the source with `sed` rather than
`grep -oP`, so it behaves the same on a macOS or BSD `grep`, and it accepts either
`## [1.0.0]` or `## 1.0.0` in the changelog.

Both guards are part of the same lesson as §10A: a check that runs *before* the
artifact is cheap and unambiguous, and the same check after it is a support ticket.

### What the first run found

| Failure | Class | Verdict |
|---|---|---|
| `examples_docs_test.go` parsed 0 keys on Windows | **Bug in our test** – `core.autocrlf` turns the heading into `## Configuration keys\r`, and the heading comparison was an exact match | Fixed: `TrimRight(raw, "\r")`, plus `TestDocumentedKeysSurviveCRLF` as a regression lock. Verified by reverting the one-line fix and watching the new test fail with `0 keys` against `21` in the LF original |
| `env_test.go` expected `/` in the expanded `~` path | **Bug in our test** – asserted the runner's path separator, not the expansion | Fixed: `filepath.Join(".ssh", "id_ed25519")` |
| `TestBootstrapRealLocalExecutor` wanted `ID=` in the log | **Bug in our test** – asserted the Linux branch on every host | Fixed: asserts the branch that applies to the host. macOS and Windows now verify their real behaviour (the soft warning) instead of being skipped |
| `UploadFile` 644, tar mode 755, vault 600 → `666` | **Not fixable in the test** – Windows has no POSIX mode bits | Asserted only where the bits exist; the vault case is below |

### The vault is a real finding, not a test artifact

The Windows failure `vault perm = 666, want 600` looked cosmetic next to the others.
It is not. The MCP server vault holds SSH passwords, easydrop writes it `0600` on
POSIX hosts, and **on Windows it is readable by every local user**: Go creates files
as `0666` and the protection would have to be an ACL entry, which easydrop does not
set. Nothing in the test could have caught this by passing – it was found *because*
the assertion failed.

The fix is not to make the test green. The test now logs the mode and states what it
means, and the limitation is documented in both READMEs under *Scope and
Limitations*. Deploying *to* Windows was never supported (targets are Debian/Ubuntu);
this only affects easydrop running as an MCP server on a Windows host, and the
documented guidance is to use `server.ssh_key` and skip `manage_server` there.
Closing it properly would mean setting a Windows DACL from Go – a deliberate feature,
not a CI fix.
