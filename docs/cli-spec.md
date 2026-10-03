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
- **Behavior:** Scans the active directory and writes a pre-filled `easydrop.toml`. Detection is local and side-effect free – no SSH, no Docker, no network. Sources are consulted in **three tiers of evidence strength**: what the image declares (ground truth) → a port written literally in the project (a fact) → an ecosystem convention (a documented default, weakest).
  - **Tier 1 – the image (wins over everything below).** A `Dockerfile` or compose file describes the container that will actually run, so it outranks any framework knowledge:
    - **compose `ports`/`expose`** – the **host** side of the first `ports:` entry of the first of `docker-compose.yml`, `docker-compose.yaml`, `compose.yml`, `compose.yaml` (short `8080:80`, `127.0.0.1:8080:80`, `8080/udp`, or long `target`/`published`; `expose:`/`target` as fallback). The service named `web`/`app`/`api`/`frontend`/`server`/… is preferred, otherwise alphabetical, so the result is deterministic. Consulted only when `driver.type = "compose"`, where `app.port` is the host-published port the Nginx vhost proxies to.
    - **`EXPOSE`** – the *last* one wins (in a multi-stage build the final stage becomes the image).
    - **A web-server final stage** – `FROM nginx` / `httpd` / `apache` / `caddy` with no `EXPOSE` implies port `80`. A Vite/React/Vue build shipped as static files lands in a `nginx:alpine` stage far more often than in a Node runtime, where the framework's dev port would be simply wrong.
    - **A port in `CMD` / `ENTRYPOINT` / `ENV`** – `--port 4000` or `PORT=8080` pins the port harder than any convention: `CMD ["next", "start", "-p", "4000"]` means 4000, and no `next`→3000 knowledge overrides it.
  - **Tier 2 – a literal in the project.** An explicit `--port N` / `-p N` in an npm script, then a bounded scan of the project's own source (≤400 files / ≤4 MB, skipping `.git`, `node_modules`, `vendor`, `dist`, `build`, `target`, …; known extensions plus extension-less `Procfile`, `Makefile`, `Dockerfile` and `.env*`): `ListenAndServe(":N")`, `Addr: ":N"`, `PORT=N`, an explicit flag (shell or JSON-array form), a Python `port=N` keyword (`uvicorn.run(app, port=8000)`, `app.run(port=5000)`), `.listen(N)`, Django `runserver 0.0.0.0:N`, gunicorn `-b 0.0.0.0:N`, Spring `server.port=N`. This tier exists because no manifest declares a port for compiled languages – `go.mod` has no port field by design, so the port lives in the code. It sits above the conventions because a port written in the app's own code is a fact, while a framework default is a guess.
  - **Tier 3 – conventions**, only when nothing states the port outright:
    - **`package.json` framework table** – `next`/`nuxt`/`@nestjs/core`/`@sveltejs/kit`/`@remix-run/serve`/`react-scripts` 3000, `vite` 5173, `@vue/cli-service` 8080, `@angular/cli` 4200, `astro` 4321, `gatsby` 9000, `serve` 3000, `http-server` 8080, `express` 3000. A `vite preview` in the `start` script resolves to 4173 instead – that is what a production container runs, unlike the 5173 dev server.
    - **Python dependencies** read from `requirements*.txt`, `pyproject.toml`, `Pipfile` and `setup.py` (`install_requires`): WSGI/ASGI servers first, since a container runs a server – `uvicorn`/`gunicorn`/`hypercorn`/`daphne` 8000, `waitress` 8080, `fastapi` 8000, `flask` 5000, `django` 8000, `sanic` 8000, `aiohttp` 8080, `pyramid` 6543, `bottle` 8080, `tornado` 8888, `streamlit` 8501, `gradio` 7860. A `flask`+`gunicorn` project therefore resolves to 8000, not Flask's 5000 dev default.
  - **Nothing detected → the key is left out.** There is no `8080` fallback: `app.port` is omitted from the generated file, the config is deliberately incomplete, and every easydrop command refuses it with a message naming the field and explaining the two failure modes (healthcheck timeout for `single`, a 502 proxy for compose/swarm). See OD-04.
  - `app.name` is the directory name, unless it is a generic placeholder (`src`, `app`, `project`, `test`, …), in which case the Go module path's last element (major-version suffix stripped) or the npm `name` is used. The detected stack (`go`, `node`, `python`, `rust`, `ruby`, `php`) is reported but configures nothing.
- **Output:** one shared report (`ScaffoldResult.Report()`), reporting the value **and its source** for every detected field, e.g. `port 9090 (source scan)`. When no port was found it prints `port NOT SET` and an `ACTION REQUIRED` block: what was searched, why nothing is defaulted, **the method to resolve it** (where the server binds: `app.listen(N)`, `ListenAndServe(":N")`, `uvicorn.run(port=N)`, `--port` in a `Procfile`/`Dockerfile CMD`, `PORT` in settings), the obligation to write `app.port` into the file, and both ways to supply it up front. It always notes that `[server].host` is still `localhost`.
- **Shared with MCP:** the MCP tool `init_project` returns **this exact text**. The two interfaces previously worded it separately and drifted; an agent receiving a weaker variant than the human is precisely the failure mode worth avoiding, so there is one `Report()` and both call it.
- **Failure mode:** malformed project files (invalid JSON/YAML, `EXPOSE banana`) degrade to the next source – `init` itself does not fail. It fails only if the file cannot be written (e.g. `easydrop.toml` exists without `--force`). The *generated* config may well be unusable on its own – that is intentional when the port is unknown, and the next command says so.
- **Flags:**
  - `--force`: Overwrite an already existing `easydrop.toml` file. This is destructive: the file is regenerated from scratch, so hand edits are lost.
  - `--port int`: The port your app listens on **inside** the container. Overrides every detection source and is reported as `explicit override`; `0` or omitted means "detect". Must be 1-65535, otherwise `init` fails. This is the supported escape hatch when detection cannot tell (OD-04) – including for an agent that has determined the port itself.

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
- **Behavior:** If `[app_name]` is omitted, `app.name` from the resolved `easydrop.toml` is used. Backed by `deploy.Logs(ctx, appName, LogOptions)` over the driver's `Logs(ctx, appName, lines, follow)` – `lines` = `--tail`, `follow` = `--follow`, and the core does the filtering (M16), so the CLI and `get_logs` cannot drift.
- **Flags:**
  - `-f, --follow`: Stream live stdout/stderr data logs from the remote host environment in real time (equivalent to `tail -f`).
  - `-n, --tail int`: Number of historical trace log lines to **scan** upon initial attachment (defaults to 100).
  - `-g, --grep string`: Case-insensitive regular expression; print only matching lines (e.g. `-g 'error|panic|fatal'`) (M16). An invalid expression fails the command – it never degrades into an unfiltered dump. When nothing matches, the reason is printed to stderr (`no lines matched (scanned N lines …)` vs `no log lines available`), because an empty result is otherwise ambiguous.

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
