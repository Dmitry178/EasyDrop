package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// schemaDoc is the easydrop.toml reference served at easydrop://docs/schema.
// It mirrors internal/models; generated-from-structs output stays a
// Milestone-9 nicety, this static copy is the locked MVP contract.
const schemaDoc = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "easydrop.toml",
  "type": "object",
  "properties": {
    "app": {"type": "object", "required": ["name"],
      "properties": {
        "name": {"type": "string", "description": "docker-compatible lowercase name"},
        "port": {"type": "integer", "minimum": 1, "maximum": 65535},
        "health_check_path": {"type": "string", "default": "/"}}},
    "server": {"type": "object", "required": ["host"],
      "properties": {
        "host": {"type": "string", "description": "IP/hostname, or localhost/127.0.0.1 for local"},
        "user": {"type": "string"},
        "ssh_key": {"type": "string", "default": "~/.ssh/id_rsa"},
        "password": {"type": "string"},
        "port": {"type": "integer", "default": 22}}},
    "build": {"type": "object",
      "properties": {
        "strategy": {"type": "string", "enum": ["remote", "local"], "default": "remote"},
        "registry": {"type": "string"},
        "image": {"type": "string"},
        "no_cache": {"type": "boolean", "default": false}}},
    "driver": {"type": "object",
      "properties": {
        "type": {"type": "string", "enum": ["single", "compose", "swarm"], "default": "single"},
        "compose_file": {"type": "string", "default": "docker-compose.yml"},
        "blue_green": {"type": "boolean", "default": false}}},
    "nginx": {"type": "object",
      "properties": {
        "domain": {"type": "string"},
        "ssl": {"type": "boolean"},
        "email": {"type": "string"}}}
  }
}`

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
	s.AddResource(
		&sdk.Resource{URI: "easydrop://docs/schema", Name: "schema",
			Description: "JSON Schema for easydrop.toml", MIMEType: "application/json"},
		func(_ context.Context, _ *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
				{URI: "easydrop://docs/schema", MIMEType: "application/json", Text: schemaDoc},
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
