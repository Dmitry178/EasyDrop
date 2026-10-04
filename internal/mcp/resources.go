package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// troubleshootingDoc is the self-healing runbook at
// easydrop://docs/troubleshooting.
const troubleshootingDoc = `# EasyDrop troubleshooting runbook

## Container fails healthcheck (deploy aborts, production untouched)
- Wrong internal port: app must listen on app.port (single-port constraint, OD-01).
- Wrong health_check_path: must return HTTP 200.
- Slow cold start: raise probing via driver tunables (2s x 10 default).
- Staging leftovers: 'docker ps -a' for *-green, 'teardown' removes them.

## Port conflicts (address already in use)
- Blue-Green uses the {host_port, host_port+1} pair; both must be free on the host.
- EasyDrop now checks the port BEFORE starting the container and reports
  "host port N is already in use by ...". Fix it by freeing that port or moving
  the pair: change app.host_port. Nothing is deployed and production keeps
  serving in the meantime.
- If the message names "a process whose owner this user cannot see", the socket
  belongs to another user - typically the root-owned docker-proxy of an
  unrelated container. 'ss -ltnp | grep :N' as root, or 'docker ps' to find it.
- The check needs 'ss' (or 'netstat') on the host. Without either it is skipped
  with a note and Docker's own "port is already allocated" is the fallback.
- A stale *-green container from a failed deploy holds a port too; 'teardown'
  removes it.

## nginx.ssl = true did nothing (localhost / internal host)
- Let's Encrypt needs a publicly resolvable domain and a reachable port 80. For
  localhost or internal DNS that is impossible, so add self_signed = true to the
  [nginx] block and set domain = "localhost" (or your internal name).
- easydrop then generates the certificate itself (P-256, with SAN), installs it
  in /etc/nginx/ssl/ with the key at 0600, and serves :443 itself. Port 80 keeps
  proxying, so http:// still works.
- The browser warns until you trust it once:
  sudo cp /etc/nginx/ssl/<domain>.crt /usr/local/share/ca-certificates/easydrop-<domain>.crt
  sudo update-ca-certificates
  easydrop prints this and never installs it for you - that needs sudo on the
  client machine and would change what every program there trusts.
- The certificate is reused until it is within 30 days of expiry, so re-trusting
  is a rare, one-time event rather than something every deploy causes.

## After teardown the domain still answers 502
- Fixed in this build: teardown now removes the managed vhost (and reloads
  nginx) when nginx.domain is set. If you ran an older easydrop, the leftover
  files are /etc/nginx/sites-available/<domain> and its symlink in
  sites-enabled/ - remove both and reload nginx by hand.

## After teardown the domain still answers 200 / nginx was not reloaded
- teardown deletes the vhost files but reloads nginx only if the config test
  (nginx -t) passes.
  Reloading a config nginx rejected would take down every other site on the
  host. The running nginx keeps serving the config it had already loaded, so the
  domain answers 502 until someone reloads. Fix the broken site config, then:
  sudo nginx -t && sudo systemctl reload nginx
  teardown reports this as ingress_reloaded=false - relay it instead of claiming
  a clean teardown.

## status shows no TLS line
- Absent ssl_status means "could not tell": no vhost for this domain, TLS not
  served for it, or nginx unreadable. It is NOT "no TLS". Inspect the running
  config yourself: sudo nginx -T | grep -A5 server_name <domain>

## Missing proxy headers / app sees http instead of https
- Nginx sets X-Forwarded-Proto; the app must trust proxy headers.

## docker build: path ... not found
- Snap-confined dockerd cannot see host /tmp: set EASYDROP_STAGING_BASE
  to a daemon-visible dir (OD-02).

## sudo password prompts on localhost
- Use 'deploy --skip-bootstrap' when docker is installed and the user is
  already in the docker group; bootstrap needs passwordless sudo otherwise.

## Rollback says "no rollback backup"
- Only Blue-Green deploys keep [app]-active-previous; direct deploys keep none.
- A rollback consumes the backup; redeploy to create a fresh one.

## SSH failures
- Check server.host/user/port, key path (~ expansion supported), or password.
- 'localhost'/'127.0.0.1' bypass SSH entirely (LocalExecutor).
- "no ssh auth methods" means nothing was configured: no server.ssh_key, no
  reachable agent, no server.password.
- "SSH_AUTH_SOCK ... is set but unreachable" means the variable exists but the
  agent behind it does not. This is the normal state for an MCP server spawned by
  an AI client with a minimal environment. Fix by pinning the key in the config
  (server.ssh_key), by server.password, or by having the client forward
  SSH_AUTH_SOCK into this server's environment.

## Snap-confined docker (Ubuntu 'snap install docker')
A snap dockerd has a private mount namespace – three separate limits:
1. No host /tmp: "unable to prepare context: path ... not found".
2. No hidden (dot-) files/dirs in $HOME: "permission denied" on ~/.easydrop/...
   for compose/swarm state, or "failed to read dockerfile" when the build
   context lives under a dot-dir.
3. $HOME inside daemon-spawned commands is snap's void path
   (/var/lib/snapd/void), so never embed '$HOME/...' in a composed command.
Fix: set EASYDROP_STAGING_BASE to a NON-hidden directory under $HOME
(e.g. ~/easydrop-staging). EasyDrop detects snap and fails fast with this
hint; compose/swarm state automatically moves to ~/easydrop/apps.
Hosts bootstrapped by easydrop use apt docker and never hit these limits.
`

func addResources(s *sdk.Server) {
	schemaText, schemaErr := GenerateSchema()
	s.AddResource(
		&sdk.Resource{URI: "easydrop://docs/schema", Name: "schema",
			Description: "JSON Schema for easydrop.toml (generated from internal/models)", MIMEType: "application/json"},
		func(_ context.Context, _ *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			if schemaErr != nil {
				return nil, schemaErr
			}
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
				{URI: "easydrop://docs/schema", MIMEType: "application/json", Text: schemaText},
			}}, nil
		})
	s.AddResource(
		&sdk.Resource{URI: "easydrop://docs/troubleshooting", Name: "troubleshooting",
			Description: "Self-healing runbook: ports, probes, proxy, snap-docker limits, sudo, rollback, SSH",
			MIMEType:    "text/markdown"},
		func(_ context.Context, _ *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
				{URI: "easydrop://docs/troubleshooting", MIMEType: "text/markdown", Text: troubleshootingDoc},
			}}, nil
		})
}
