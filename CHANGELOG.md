# Changelog

All notable changes to EasyDrop are recorded in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [semantic versioning](https://semver.org/spec/v2.0.0.html).

## 1.0.0 - 2026-10-04

First release: everything below ships in this version. Scope and limitations are
documented in the [README](README.md); design rationale and closed decisions in
[`docs/implementation.md`](docs/implementation.md).

### Deploy

- Single binary, two interfaces – the `easydrop` CLI and an MCP server – over one
  shared operation core.
- Single-container deploys to any Debian/Ubuntu host over SSH, or to the local
  Docker daemon with no SSH at all.
- **Zero-downtime Blue-Green swaps** (`driver.blue_green`): stage on the free port
  of the `{host_port, host_port+1}` pair, health-check, flip the proxy, keep the
  previous release as a rollback backup. `rollback` restores it and consumes it.
- **Compose and Swarm stacks** (`driver.type`), with the same ingress and rollback
  behaviour.
- **Two build strategies**: build on the target host, or build here and push to a
  registry the target pulls from.
- `app.port` (in-container) and `app.host_port` (published), so an image with a
  fixed internal port can still be published anywhere.

### Configuration and `init`

- `easydrop init` scans the project and reports every detected value with its
  source: Dockerfile `EXPOSE`, compose `ports`/`expose`, `--port`, `PORT=` in
  `.env`, framework conventions, and a bounded scan of the project's own source.
- When no port can be determined, **none is written** – a wrong port fails the
  healthcheck or returns a 502, so an honest "not set" beats a plausible guess.
- `--port` overrides detection; the CLI and MCP share one report text.
- **Secrets stay out of the config**: `${VAR}` interpolation from the environment or
  from `.easydrop.env` / `.env` beside the config, resolved before any connection.

### Ingress and TLS

- Managed Nginx reverse proxy, validated with `nginx -t` before every reload, so a
  rejected config never reaches the running server.
- Let's Encrypt over HTTP-01, with renewal left to Certbot's own timer.
- **Self-signed certificates for `localhost` and internal DNS**
  (`nginx.self_signed`): generated in-process (ECDSA P-256, SubjectAltName),
  installed in `/etc/nginx/ssl/` with the key at `0600`, served on `:443`.
  Reused until inside 30 days of expiry. Never added to your system trust store –
  the command is printed instead.
- `status` reports the certificate **as nginx is serving it**: expiry, an
  `EXPIRING` warning and a `DOES NOT COVER` mismatch, read from the running config
  and the certificate file it names. Identical for self-signed and ACME.
- `teardown` removes the managed vhost with the containers and reloads nginx.
  Named volumes, the built image and any Let's Encrypt certificate are never
  deleted; a self-signed one needs `--purge`.

### Operability

- **Host port pre-flight**: a busy port fails before the container starts, naming
  the port, the squatter and the remedy.
- **Zero-to-hero bootstrap**: installs Docker Engine and Compose only when
  missing, adds the deploy user to the docker group, aligns ufw only where ufw
  exists, and never clobbers an existing setup.
- Detects **snap-confined Docker daemons** and fails fast with the workaround.
- **Health checks over HTTP or TLS**: the probe falls back to HTTPS in the same
  attempt, so an app that answers only over TLS deploys normally. Redirects are
  never followed, deliberately. Pin with `app.health_check_scheme`.
- **Log filtering** (`logs --grep`, `get_logs {filter}`) with `scanned`/`matched`
  counts, so "0 matched" is never read as "healthy".
- An unreachable `SSH_AUTH_SOCK` is named as such instead of degrading to a bare
  "no ssh auth methods".

### MCP server

- Seven tools – `init_project`, `deploy_app`, `get_status`, `get_logs`,
  `rollback_app`, `teardown_app`, `manage_server` – and two resources, on the
  official `modelcontextprotocol/go-sdk`.
- **Behaviour annotations on every tool**, so a client can tell a read from a write
  before it calls one.
- Resources: a **JSON Schema for `easydrop.toml` generated from the same structs
  the parser uses** (so it cannot drift), and a troubleshooting runbook.
- **Encrypted server vault** (AES-256-GCM, scrypt, `0600`), keyed only by
  `EASYDROP_VAULT_PASSWORD`, failing closed rather than prompting.

### Documentation

- `README.md` / `README_RU.md`, `PRD.md`, `ARCHITECTURE.md`, `docs/`, and nineteen
  annotated configs in [`examples/`](examples/) – every one validated by CI against
  the real parser, with the key reference table checked against the config structs.
