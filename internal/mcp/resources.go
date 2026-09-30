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
- Blue-Green uses the {Port, Port+1} pair; both must be free on the host.
- 'docker ps' to find the squatter; change app.port or stop it.

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
			Description: "Self-healing runbook: ports, probes, proxy, snap-docker, sudo, rollback, SSH",
			MIMEType:    "text/markdown"},
		func(_ context.Context, _ *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
				{URI: "easydrop://docs/troubleshooting", MIMEType: "text/markdown", Text: troubleshootingDoc},
			}}, nil
		})
}
