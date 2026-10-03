# EasyDrop configuration examples

One `easydrop.toml` per case, covering every option the tool supports. Each
file is self-documenting: the comments explain *why* the values are what they
are, not just what they are.

* [`99-full-reference.toml`](99-full-reference.toml) – every key, with its default. Start here to learn the schema.
* The numbered files are runnable scenarios. Copy the closest one and edit it.

> ### Passwords: one question decides how you write them
>
> **Does this `easydrop.toml` go into git?**
>
> **Yes** – a shared repo, a CI checkout. Keep the file secret-free:
> 1. **ssh-agent** – nothing secret in the project at all:
>    `eval "$(ssh-agent -s)" && ssh-add ~/.ssh/id_ed25519`, then leave
>    `server.ssh_key` and `server.password` empty. Also the only way to use a
>    passphrase-protected key.
> 2. **`${VAR}` from a secret file** – `password = "${EASYDROP_SSH_PASSWORD}"`
>    here, the value in the git-ignored `.easydrop.env` (or `.env`) next to the
>    config. See [§7](#7-keeping-secrets-out-of-git) and
>    [`20-secrets-from-env.toml`](20-secrets-from-env.toml).
>
> A literal `server.password = "hunter2"` is **discouraged** for a committed
> config: it is a live credential in git history, in every clone, in any CI log
> that prints a diff, and in every backup – and rotating the password later
> removes it from none of those.
>
> **No** – local experiments, a throwaway box, a personal project you keep out
> of git. A literal password is **fine**, and the fastest route: no repository
> ever sees the file, so there is nothing to leak. Two cheap precautions:
> `chmod 600 easydrop.toml` and `echo easydrop.toml >> .git/info/exclude`.
> See [`04-remote-ssh-password.toml`](04-remote-ssh-password.toml).
>
> `server.ssh_key` is only a *path*, so it is safe to commit either way.

---

## How to use one

The directory containing your config **is** the build workspace – EasyDrop
archives it and ships it to the host. So copy the example into your project
rather than pointing at it in place:

```bash
cp examples/06-remote-nginx-ssl.toml /path/to/my-app/easydrop.toml
$EDITOR /path/to/my-app/easydrop.toml     # set server.host, user, domain, email
cd /path/to/my-app && easydrop deploy
```

Read-only commands work with any path, so you can try the examples as-is:

```bash
easydrop status -c examples/02-localhost-dev.toml
```

To generate a config from your own project instead of copying one:

```bash
easydrop init            # refuses to overwrite; add --force to replace
```

`init` scans the project and reports where every value came from – the listen
port from `Dockerfile EXPOSE`, a compose `ports` mapping, `package.json` (an
explicit script port, else the framework's) or a bounded look at your own source
for languages whose manifests carry no port – plus the detected stack and the
driver implied by a compose file. If nothing in the project states a port, it
writes **no `app.port` at all** and prints `port NOT SET` with an
`ACTION REQUIRED` block: there is no 8080 fallback, because a wrong port builds
fine and only breaks the deploy (healthcheck timeout for `single`, a silent 502
behind the proxy for compose/swarm). The generated config is intentionally
incomplete until you fill the port in – by hand, or with `easydrop init --port N`,
which overrides detection. The MCP tool `init_project` takes the same port as an
argument and returns the same report the CLI prints. `app.name` is the directory
name, unless that is a placeholder like `src` or `app`, in which case the Go
module path or the npm name is used instead. Full rules in
[`docs/cli-spec.md`](../docs/cli-spec.md) §1.1.

---

## Index

| File | Case | Highlights |
| --- | --- | --- |
| [`01-minimal.toml`](01-minimal.toml) | Smallest possible config | only the two required fields |
| [`02-localhost-dev.toml`](02-localhost-dev.toml) | Local development | local executor, `host_port` split, custom healthcheck |
| [`03-remote-ssh-key.toml`](03-remote-ssh-key.toml) | Remote VPS, key auth | the common production shape, no domain |
| [`04-remote-ssh-password.toml`](04-remote-ssh-password.toml) | Password auth | three ways to supply it, from secret-free to throwaway |
| [`05-remote-ssh-custom-port.toml`](05-remote-ssh-custom-port.toml) | Hardened SSH | `server.port = 2222`, `host_port` ≠ `port` |
| [`06-remote-nginx-ssl.toml`](06-remote-nginx-ssl.toml) | **Flagship**: VPS + domain + TLS | bootstrap → build → Nginx → Let's Encrypt |
| [`07-blue-green.toml`](07-blue-green.toml) | Zero-downtime deploys | `blue_green`, backup container, `rollback` target |
| [`08-build-no-cache.toml`](08-build-no-cache.toml) | Bust a stale layer cache | `build.no_cache` |
| [`09-healthcheck-path.toml`](09-healthcheck-path.toml) | Readiness endpoint | `health_check_path = "/healthz"` |
| [`10-nginx-plain-http.toml`](10-nginx-plain-http.toml) | Domain, no TLS | reverse proxy only, `ssl = false` |
| [`11-build-local-registry.toml`](11-build-local-registry.toml) | Build here, push there | `build.strategy = "local"`, `registry` |
| [`12-build-local-registry-pinned-tag.toml`](12-build-local-registry-pinned-tag.toml) | CI-friendly tagging | `build.image = "web:2.1"` |
| [`13-compose.toml`](13-compose.toml) | Multi-service stack | `driver.type = "compose"` |
| [`14-compose-custom-file.toml`](14-compose-custom-file.toml) | Env-specific compose file | `docker-compose.prod.yml` |
| [`15-swarm.toml`](15-swarm.toml) | Swarm stack | `driver.type = "swarm"` |
| [`16-compose-nginx-ssl.toml`](16-compose-nginx-ssl.toml) | Compose + domain + TLS | the realistic multi-service production setup |
| [`20-secrets-from-env.toml`](20-secrets-from-env.toml) | Secrets without git | `${VAR}`, `.easydrop.env`, defaults |
| [`99-full-reference.toml`](99-full-reference.toml) | Schema reference | all keys, all defaults |

Examples that touch SSH credentials – `03`, `04`, `20`, `99` – all frame the
choice the same way: if the config is committed, it carries a *path* or a
`${VAR}` reference; if it never enters a repository, a literal is acceptable.
Each of them repeats the reasoning inline.

---

## 1. Getting started

**[`01-minimal.toml`](01-minimal.toml)** – `app.name`, `app.port` and
`server.host` are the only required fields. Everything else is defaulted:
`host_port` → `port`, `health_check_path` → `/`, `server.port` → `22`,
`server.ssh_key` → `~/.ssh/id_rsa`, `build.strategy` → `remote`,
`driver.type` → `single`, `nginx.ssl` → `false`.

**[`02-localhost-dev.toml`](02-localhost-dev.toml)** – deploy to the local
Docker daemon. `server.host = "localhost"` (or `127.0.0.1`) selects the local
executor and no SSH is used at all, so `server.user` and `server.ssh_key` are
ignored. Shows the port split (`port = 3000` inside the container,
`host_port = 8080` on the host) and a `/healthz` probe. This is the config
`make smoke` exercises.

## 2. Target hosts

> **Credentials on this path.** `server.password` is the last of three auth
> methods (key file → ssh-agent → password) and the only one that can end up
> being a literal. Whether that is a problem depends on one thing: whether the
> config is committed. Committed → keep it secret-free (ssh-agent or `${VAR}`).
> Never committed → a literal is fine. Examples `03`, `04`, `20` and `99` all
> spell this out inline; details in [§7](#7-keeping-secrets-out-of-git).

**[`03-remote-ssh-key.toml`](03-remote-ssh-key.toml)** – key auth over SSH.
Requires that the user can log in with the key and has passwordless `sudo`
(or pass `--skip-bootstrap`). The workspace is uploaded over SFTP, built on the
host, and run as a single container. `server.ssh_key` is a *path*, so this
config is committable as-is; leaving it empty and using ssh-agent is an equally
good alternative.

**[`04-remote-ssh-password.toml`](04-remote-ssh-password.toml)** – the host
where you cannot install a public key. The example ships no active credential
(`ssh_key` and `password` are empty) and the header walks through all three
routes: ssh-agent, `password = "${EASYDROP_SSH_PASSWORD}"` with the value in
`.easydrop.env`, and – for a config that never enters a VCS – a plain literal
password. Uncomment whichever fits.

**[`05-remote-ssh-custom-port.toml`](05-remote-ssh-custom-port.toml)** – a
hardened host on port `2222`. `server.port` is the *SSH* port; the app port is
`app.port`. Note also that `host_port` may differ from `port`.

**[`06-remote-nginx-ssl.toml`](06-remote-nginx-ssl.toml)** – everything at
once: SSH, remote build, single container, Nginx reverse proxy and a Let's
Encrypt certificate. Requires DNS to already resolve to `server.host` and
inbound ports **80 and 443** (HTTP-01 needs 80 even though traffic ends up on
443).

## 3. Build strategies

`build.strategy` chooses where the image is produced.

**Remote (default)** – [`03`](03-remote-ssh-key.toml), and every compose/swarm
example. The workspace is archived and uploaded, and the host runs
`docker build`. No registry, no push, no pull. Your sources *do* land on the
server.

**Local** – [`11`](11-build-local-registry.toml) and
[`12`](12-build-local-registry-pinned-tag.toml). The image is built on the
machine running EasyDrop, pushed to `build.registry`, and pulled by the host.
Only the image crosses the wire, so the server never sees your source tree.
Requires:

* `build.registry` without a scheme – `ghcr.io/myorg`,
  `registry.example.com:5000/team`; `https://…` is rejected
* `driver.type = "single"` (compose/swarm build their own services on the node)
* registry credentials both locally (`docker login`) and on the host

The reference is `[registry/]<image>:<tag>`, where `<image>` is `build.image`
or, when empty, `app.name`; the tag defaults to `latest`.

**Cache control** – [`08-build-no-cache.toml`](08-build-no-cache.toml) sets
`build.no_cache = true` for every deploy. Prefer the per-run
`easydrop deploy --no-cache` for one-off debugging.

## 4. Orchestration drivers

`driver.type` picks how the app is run on the host.

**`single`** (default) – one container named `<app>-active`. A plain deploy
replaces it in place (brief downtime). With `blue_green = true`
([`07`](07-blue-green.toml)) the new container boots on the port pair
`{host_port, host_port+1}`, waits for its healthcheck, flips ingress, and keeps
the old one stopped as `<app>-active-previous` – the target of
`easydrop rollback`.

**`compose`** – [`13`](13-compose.toml), [`14`](14-compose-custom-file.toml),
[`16`](16-compose-nginx-ssl.toml). The workspace is shipped, built on the host
with `docker compose build`, and run as project `easydrop-<app.name>`.

**`swarm`** – [`15-swarm.toml`](15-swarm.toml). Same shipping, but
`docker stack deploy -c <file> easydrop-<app.name>`, with `docker swarm init`
run first if the node is not already in a swarm.

Two differences matter when writing a compose or swarm config:

* `app.port` is the port **published on the host**, because that is what the
  Nginx vhost proxies to. There is no `host_port` split for these drivers, so
  the published port and `app.port` must agree.
* Readiness is the stack's own (`compose ps` / desired replicas), not an HTTP
  probe, so `health_check_path` is unused.

`driver.compose_file` names the file for both, defaulting to
`docker-compose.yml`. Only that file is used – it is invoked as
`docker compose -p easydrop-<app> -f <staged>/<file>`, so no stray
`docker-compose.yml` or `COMPOSE_FILE` can interfere.

`blue_green` and `build.strategy = "local"` are **`single`-only**; setting them
with compose or swarm is a hard error, not a silent downgrade.

## 5. Ingress and TLS

Ingress is controlled entirely by `nginx.domain`:

* **empty** – no reverse proxy at all. Nothing is written to `/etc/nginx`; the
  container just runs ([`03`](03-remote-ssh-key.toml)).
* **set, `ssl = false`** – renders
  `/etc/nginx/sites-available/<domain>`, symlinks it into `sites-enabled`, runs
  `nginx -t`, then reloads ([`10`](10-nginx-plain-http.toml)). A failing
  `nginx -t` aborts *before* the reload, so the previous live config keeps
  serving.
* **set, `ssl = true`** – the same, plus a Let's Encrypt certificate for the
  domain ([`06`](06-remote-nginx-ssl.toml), [`16`](16-compose-nginx-ssl.toml)).
  Set `nginx.email` as the ACME contact: without it nobody warns you before the
  certificate expires.

`app.health_check_path`
([`09`](09-healthcheck-path.toml)) is probed at
`http://127.0.0.1:<host_port><path>` after the container starts – 10 attempts,
2 seconds apart, then the deploy fails. Point it at a cheap, dependency-light
endpoint: one that needs the database turns a slow start into a failed deploy.

## 6. Full reference

[`99-full-reference.toml`](99-full-reference.toml) lists every key with its
default, its constraints and its failure mode. It is a map, not a scenario.

## 7. Keeping secrets out of git

`easydrop.toml` is a file you usually commit. So the first question is not
"how do I hide the password" but "does this file live in a repository at all".

| Config | Method | Secret lives in |
| --- | --- | --- |
| committed | ssh-agent (preferred) | nowhere in the project |
| committed | `password = "${EASYDROP_SSH_PASSWORD}"` | `.easydrop.env` (or `.env`), git-ignored |
| **not committed** | `password = "hunter2"` | this file, `chmod 600`, excluded from VCS |

A literal password in a **committed** config is discouraged: it is a live
credential in the history, in every clone, in any CI log that prints a diff, and
in every backup – rotating it afterwards removes it from none of those. A
literal in a config that **never enters a VCS** is fine, because nothing
reproduces the file and nothing stores it but the machine it lives on. For a
throwaway box that is the fastest option, and it is documented as such in
[`04-remote-ssh-password.toml`](04-remote-ssh-password.toml).

If you do keep a literal, two precautions cost nothing:

```bash
chmod 600 easydrop.toml
echo easydrop.toml >> .git/info/exclude   # so a later `git add .` skips it
```

**A. Let `ssh-agent` hold the credential** – nothing secret in the file at all:

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519
```

Leave `server.ssh_key` and `server.password` empty; EasyDrop picks the agent up
through `SSH_AUTH_SOCK` (`internal/core/ssh_client.go`, auth order is key file →
agent → password). This is also the only way to use a passphrase-protected key,
since encrypted private keys cannot be parsed directly. See
[`04-remote-ssh-password.toml`](04-remote-ssh-password.toml).

The agent socket is an environment variable, and an MCP server is a *spawned
child*: an AI client may start it without `SSH_AUTH_SOCK`, or with it pointing at
an agent the child cannot reach. `easydrop deploy` in your terminal then works
while the same deploy through the assistant does not. EasyDrop names that case
explicitly (`SSH_AUTH_SOCK=… is set but unreachable`) instead of reporting a bare
"no ssh auth methods". Outside MCP, prefer option A below when you can – a key
path in the config needs no environment at all.

**B. Interpolate from a secret file** – see
[`20-secrets-from-env.toml`](20-secrets-from-env.toml):

```toml
[server]
password = "${EASYDROP_SSH_PASSWORD}"
```

```bash
printf 'EASYDROP_SSH_PASSWORD=hunter2\n' > .easydrop.env   # git-ignored
```

| Syntax | Meaning |
| --- | --- |
| `${VAR}` | required – unset or empty fails immediately, naming the field, **never the value** |
| `${VAR:-default}` | the default applies when `VAR` is unset or empty |
| `$$` | a literal `$` |

`.easydrop.env` is easydrop's own file; `.env` works too and is handy when the
project already has one for docker compose – `.easydrop.env` wins if both
exist. Both are git-ignored and, since EasyDrop reads them itself, both are
excluded from the archive shipped to the target host: a deploy never carries
the credential it authenticates with. A compose project that genuinely needs
`.env` inside the bundle re-includes it with `!.env` in `.dockerignore`.

Precedence: real environment > `.easydrop.env` > `.env`, so CI can inject a
value with `EASYDROP_SSH_PASSWORD=... easydrop deploy` and write no file at all.
Interpolation applies to the string fields only and happens before validation –
a missing variable aborts the command before any SSH connection is attempted.

The encrypted vault (`manage_server`, `~/.easydrop/servers.vault`) is a separate
MCP-only store and is **not** read during deploys – do not expect a
`manage_server` call to feed `easydrop deploy`, and do not use it as a reason to
write a literal password in the config.

---

## Configuration keys

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `app.name` | string | – | **required**; `^[a-z0-9]+(?:[._-][a-z0-9]+)*$` |
| `app.port` | int | – | **required**; 1–65535; in-container for `single`, host-published for compose/swarm |
| `app.host_port` | int | `app.port` | 1–65534; Blue-Green needs `{host_port, host_port+1}` |
| `app.health_check_path` | string | `/` | must answer `200`; `single` only |
| `server.host` | string | – | **required**; `localhost`/`127.0.0.1` = local executor |
| `server.user` | string | – | required for remote hosts, ignored for localhost |
| `server.ssh_key` | string | `~/.ssh/id_rsa` | only the `~/` form is expanded |
| `server.password` | string | – | last-resort auth; in a committed config use `${VAR}` – see [§7](#7-keeping-secrets-out-of-git) |
| `server.port` | int | `22` | SSH port |
| `build.strategy` | string | `remote` | `remote` or `local` |
| `build.registry` | string | – | required for `local`; no scheme |
| `build.image` | string | `app.name` | `name`, `name:tag` or `ns/name:tag` |
| `build.no_cache` | bool | `false` | `--no-cache` forces true |
| `driver.type` | string | `single` | `single`, `compose` or `swarm` |
| `driver.compose_file` | string | `docker-compose.yml` | compose and swarm |
| `driver.blue_green` | bool | `false` | `single` only; `--blue-green` forces true |
| `nginx.domain` | string | `""` | empty = no ingress |
| `nginx.ssl` | bool | `false` | requires a non-empty domain |
| `nginx.email` | string | `""` | ACME contact |

Unknown keys are **silently ignored** by the TOML parser, so a typo surfaces as
a confusing "missing" error later. The MCP resource `easydrop://docs/schema`
is generated from the same structs the parser uses, so it can validate a file –
see [`docs/mcp-spec.md`](../docs/mcp-spec.md).

## Validation rules

These fail fast, with a message naming the field:

* `app.name` must be lowercase and docker-compatible – it becomes a container,
  image, project and stack name.
* `app.port` must be 1–65535; `app.host_port` 1–65534 (65535 would leave no
  port for the Blue-Green pair).
* `build.strategy` must be `remote` or `local`; `driver.type` must be `single`,
  `compose` or `swarm`.
* `blue_green` with compose/swarm is an error; so is `build.strategy = "local"`
  with compose/swarm.
* `build.strategy = "local"` without `build.registry` fails when the build
  starts; a registry containing `://` is rejected.

Beyond the config itself, the host is checked before a container is started:
a Blue-Green deploy needs the *free* port of the `{host_port, host_port+1}`
pair, and a squatter on it (an unrelated container, a leftover `*-green`, any
other process) fails the deploy with the port and the owner named – production
keeps serving. The check uses `ss` or `netstat` on the target and is skipped
with a note when neither exists.

## Environment variables

None of these belong in `easydrop.toml` – they are machine-level settings.

| Variable | Default | Purpose |
| --- | --- | --- |
| `EASYDROP_STAGING_BASE` | `/tmp/easydrop` | host staging root for uploaded workspaces and rendered Nginx configs |
| `EASYDROP_VAULT_PASSWORD` | – | unlocks the server vault; required for `manage_server`, no prompt, fails closed |
| `EASYDROP_SERVERS_FILE` | `~/.easydrop/servers.vault` | vault location |
| `SSH_AUTH_SOCK` | – | standard ssh-agent socket; when set, the agent is tried after the key file. A set-but-unreachable socket is reported by name – the usual case is an MCP server spawned by an AI client with a minimal environment |

`${VAR}` references in the config resolve against the real environment first and
the `.env` / `.easydrop.env` files in the config's directory after that – see
[§7](#7-keeping-secrets-out-of-git). Those are files, not variables, so they
are not listed here.

`EASYDROP_STAGING_BASE` matters on **snap-confined Docker** hosts, which run
in a private mount namespace: they cannot see `/tmp` and inject their own
`$HOME` into their commands. Point the staging base at a non-hidden path inside
`$HOME` (e.g. `~/easydrop-staging`) so builds can read the context. EasyDrop
detects the situation and explains it instead of failing opaquely; `make smoke`
sets it automatically. Details in
[`docs/implementation.md §10`](../docs/implementation.md).

## Keeping these examples honest

`internal/config/examples_test.go` runs on every `make verify`. It parses each
file through the real parser, rejects unknown section/key names, and asserts the
driver, strategy, ingress and Blue-Green values each example is supposed to
demonstrate – so a renamed key or a stale example fails CI instead of reaching
a user. When you add an example, add it to `exampleCases` in that test.
