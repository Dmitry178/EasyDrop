// Package mcp exposes the EasyDrop operation core (internal/deploy) as a
// Model Context Protocol server over stdio, using the official
// modelcontextprotocol/go-sdk. Secrets from manage_server are never logged.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"easydrop/internal/version"
)

// NewServer builds the MCP server with all tools and resources registered.
func NewServer() *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "easydrop", Version: version.Version}, nil)

	sdk.AddTool(s, &sdk.Tool{Name: "init_project", Description: "Scan the project and generate easydrop.toml: detects the listen port (Dockerfile EXPOSE, compose ports, --port flag, PORT= in .env, framework conventions, or the project's own source), the stack, and the driver. Nothing is guessed – when no port can be found, [app].port is left unset and deploy_app fails until you set it, so resolve it by reading the code or pass the port via this tool's `port` argument. The result states the source of every detected value."}, handleInit)
	sdk.AddTool(s, &sdk.Tool{Name: "deploy_app", Description: "Build and deploy the app to the target host (bootstrap, build, Blue-Green/single swap, ingress, SSL)"}, handleDeploy)
	sdk.AddTool(s, &sdk.Tool{Name: "get_status", Description: "Show the deployed app status"}, handleStatus)
	sdk.AddTool(s, &sdk.Tool{Name: "get_logs", Description: "Read container logs (snapshot or follow)"}, handleLogs)
	sdk.AddTool(s, &sdk.Tool{Name: "rollback_app", Description: "Restore the backup kept by the last Blue-Green deploy"}, handleRollback)
	sdk.AddTool(s, &sdk.Tool{Name: "teardown_app", Description: "Remove the deployment (containers, backups, compose/swarm stack state); volumes are kept"}, handleTeardown)
	sdk.AddTool(s, &sdk.Tool{Name: "manage_server", Description: "Add or remove a target host record in the local server store"}, handleManageServer)

	addResources(s)
	return s
}

// Run serves MCP over stdio until the client disconnects.
func Run(ctx context.Context) error {
	return NewServer().Run(ctx, &sdk.StdioTransport{})
}

// textOut is the uniform human-readable tool payload.
type textOut struct {
	Message string `json:"message" jsonschema:"human-readable result summary"`
}

func okResult(msg string) (*sdk.CallToolResult, textOut, error) {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: msg}},
	}, textOut{Message: msg}, nil
}

func errResult(msg string) (*sdk.CallToolResult, textOut, error) {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: msg}},
		IsError: true,
	}, textOut{Message: msg}, nil
}
