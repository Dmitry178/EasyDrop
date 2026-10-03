// Package mcp exposes the EasyDrop operation core (internal/deploy) as a
// Model Context Protocol server over stdio, using the official
// modelcontextprotocol/go-sdk. Secrets from manage_server are never logged.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"easydrop/internal/version"
)

// tool builds a Tool definition carrying its behaviour hints.
//
// The hints are the only read/write signal an MCP client gets before the
// arguments are even known, so every tool declares them explicitly rather
// than inheriting the protocol defaults (which are readOnly=false,
// destructive=true). Two tools (get_status, get_logs) are pure reads and say
// so; the mutating ones are marked so a client that supports confirmation can
// gate them. Per the spec these are hints – never a security boundary – but
// the defaults being "assume destructive" would misdescribe the read tools,
// which is the case that matters for an agent deciding what to call next.
func tool(name, title, description string, ann sdk.ToolAnnotations) *sdk.Tool {
	ann.Title = title
	return &sdk.Tool{Name: name, Description: description, Annotations: &ann}
}

// boolPtr returns a pointer to b – ToolAnnotations models the two tri-state
// hints (destructive, openWorld) as *bool so that "unset" and "false" stay
// distinguishable on the wire.
func boolPtr(b bool) *bool { return &b }

// readOnly is the annotation set shared by the tools that never mutate the
// target host or the local store.
func readOnly() sdk.ToolAnnotations {
	return sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
}

// mutating describes a tool that changes state but is safe to repeat with the
// same arguments.
func mutating(destructive bool) sdk.ToolAnnotations {
	return sdk.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: boolPtr(destructive),
		IdempotentHint:  true,
		OpenWorldHint:   boolPtr(true),
	}
}

// NewServer builds the MCP server with all tools and resources registered.
func NewServer() *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "easydrop", Version: version.Version}, nil)

	sdk.AddTool(s, tool("init_project", "Initialize easydrop.toml",
		"Read-only with respect to the target host: scans the project and writes easydrop.toml. It detects the listen port (Dockerfile EXPOSE, compose ports, --port flag, PORT= in .env, framework conventions, or the project's own source), the stack, and the driver. Nothing is guessed – when no port can be found, [app].port is left unset and deploy_app fails until you set it, so resolve it by reading the code or pass the port via this tool's `port` argument. The result states the source of every detected value. With force=true an existing easydrop.toml is regenerated from scratch and hand edits are lost, so confirm with the user before setting it.",
		mutating(true)), handleInit)
	sdk.AddTool(s, tool("deploy_app", "Build and deploy the app",
		"Build and deploy the app to the target host (bootstrap, build, Blue-Green/single swap, ingress, SSL). Mutates the target host: restarts the running container and, with Blue-Green, replaces the rollback backup. Deploying is additive but not free – it may take minutes and drop the previous image, so confirm with the user before calling it unless they asked for a deploy.",
		mutating(false)), handleDeploy)
	sdk.AddTool(s, tool("get_status", "Show the deployed app status",
		"Show the deployed app status. Safe to call at any time – preferred over deploy_app for answering questions about what is running.",
		readOnly()), handleStatus)
	sdk.AddTool(s, tool("get_logs", "Read container logs",
		"Read container logs (snapshot or follow). Safe to call at any time. Use `tail` to bound the output and `filter` (a case-insensitive regular expression) to keep only the lines that matter – e.g. filter: \"error|panic|fatal|traceback\" – so a failing container can be diagnosed without flooding the context window.",
		readOnly()), handleLogs)
	sdk.AddTool(s, tool("rollback_app", "Roll back to the previous release",
		"Restore the backup kept by the last Blue-Green deploy. Destructive and NOT idempotent: it consumes the backup, so a second call fails until the next replacing deploy, and it replaces the currently running container. Confirm with the user before calling it.",
		sdk.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(true),
		}), handleRollback)
	sdk.AddTool(s, tool("teardown_app", "Remove the deployment",
		"Remove the deployment (containers, backups, compose/swarm stack state); volumes are kept. Destructive: the app stops serving traffic and the Blue-Green rollback backup is destroyed. Idempotent – tearing down an app that is not deployed is not an error. Confirm with the user before calling it.",
		mutating(true)), handleTeardown)
	sdk.AddTool(s, tool("manage_server", "Manage the target host store",
		"Add or remove a target host record in the local server store. Removing a record forgets the credentials for that host and changes nothing on the target host itself, so a mistake is only recoverable by entering the host again – confirm with the user before removing.",
		mutating(true)), handleManageServer)

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
