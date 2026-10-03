package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"easydrop/internal/deploy"
)

const defaultConfigPath = "./easydrop.toml"

// followLogCap bounds MCP follow collection so a forgotten stream cannot
// grow memory without limit; the client cancels the context to stop earlier.
const followLogCap = 2000

type initInput struct {
	Force bool `json:"force,omitempty" jsonschema:"overwrite an existing easydrop.toml (regenerates it from scratch, discarding hand edits)"`
	Port  int  `json:"port,omitempty" jsonschema:"the port the app listens on INSIDE the container; overrides detection. Omit to detect it – when nothing in the project states it, easydrop writes no port and deploy_app fails until one is set"`
}

func handleInit(ctx context.Context, _ *sdk.CallToolRequest, in initInput) (*sdk.CallToolResult, textOut, error) {
	_ = ctx
	res, err := deploy.Init(".", deploy.InitOptions{Force: in.Force, Port: in.Port})
	if err != nil {
		return errResult(fmt.Sprintf("init failed: %v", err))
	}
	// The same Report() the CLI prints. An agent must not receive a weaker
	// description of the outcome than a human does – the port-missing case in
	// particular has to reach it with the method, not just the prohibition.
	return okResult(strings.TrimRight(res.Report(), "\n"))
}

type deployInput struct {
	ConfigPath string `json:"config_path,omitempty" jsonschema:"path to easydrop.toml"`
	NoCache    bool   `json:"no_cache,omitempty" jsonschema:"bypass the Docker build cache for this run"`
	BlueGreen  bool   `json:"blue_green,omitempty" jsonschema:"zero-downtime Blue-Green swap for this run"`
}

func handleDeploy(ctx context.Context, _ *sdk.CallToolRequest, in deployInput) (*sdk.CallToolResult, textOut, error) {
	path := strings.TrimSpace(in.ConfigPath)
	if path == "" {
		path = defaultConfigPath
	}
	if err := deploy.Run(ctx, deploy.Options{
		ConfigPath: path, NoCache: in.NoCache, BlueGreen: in.BlueGreen,
	}, nil); err != nil {
		return errResult(fmt.Sprintf("deploy failed: %v", err))
	}
	return okResult("deployed successfully")
}

type statusInput struct {
	AppName string `json:"app_name" jsonschema:"application name to inspect"`
}

func handleStatus(ctx context.Context, _ *sdk.CallToolRequest, in statusInput) (*sdk.CallToolResult, statusOut, error) {
	if strings.TrimSpace(in.AppName) == "" {
		return mcpErr[statusOut]("app_name is required")
	}
	st, err := deploy.Status(ctx, defaultConfigPath, in.AppName)
	if err != nil {
		return mcpErr[statusOut](fmt.Sprintf("status failed: %v", err))
	}
	msg := fmt.Sprintf("status=%s uptime=%s", st.Status, st.Uptime)
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: msg}},
	}, statusOut{Status: st.Status, Uptime: st.Uptime, SSLStatus: st.SSLStatus}, nil
}

type statusOut struct {
	Status    string `json:"status" jsonschema:"Up, Down or Restarting"`
	Uptime    string `json:"uptime,omitempty" jsonschema:"human uptime when Up"`
	SSLStatus string `json:"ssl_status,omitempty" jsonschema:"TLS info when managed"`
}

type logsInput struct {
	AppName string `json:"app_name" jsonschema:"application name to read logs from"`
	Tail    int    `json:"tail,omitempty" jsonschema:"lines of history, defaults to 50"`
	Follow  bool   `json:"follow,omitempty" jsonschema:"keep streaming until the client cancels"`
}

func handleLogs(ctx context.Context, _ *sdk.CallToolRequest, in logsInput) (*sdk.CallToolResult, logsOut, error) {
	if strings.TrimSpace(in.AppName) == "" {
		return mcpErr[logsOut]("app_name is required")
	}
	tail := in.Tail
	if tail <= 0 {
		tail = 50
	}
	limit := 0
	if in.Follow {
		limit = followLogCap
	}
	lines, err := deploy.Logs(ctx, defaultConfigPath, in.AppName, tail, in.Follow, limit)
	if err != nil {
		return mcpErr[logsOut](fmt.Sprintf("logs failed: %v", err))
	}
	joined := strings.Join(lines, "\n")
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: joined}},
	}, logsOut{Lines: lines}, nil
}

type logsOut struct {
	Lines []string `json:"lines" jsonschema:"collected log lines"`
}

type rollbackInput struct {
	AppName    string `json:"app_name" jsonschema:"application name to roll back"`
	ConfigPath string `json:"config_path,omitempty" jsonschema:"path to easydrop.toml"`
}

func handleRollback(ctx context.Context, _ *sdk.CallToolRequest, in rollbackInput) (*sdk.CallToolResult, textOut, error) {
	if strings.TrimSpace(in.AppName) == "" {
		return errResult("app_name is required")
	}
	path := strings.TrimSpace(in.ConfigPath)
	if path == "" {
		path = defaultConfigPath
	}
	if err := deploy.Rollback(ctx, path, in.AppName); err != nil {
		return errResult(fmt.Sprintf("rollback failed: %v", err))
	}
	return okResult(fmt.Sprintf("%s rolled back", in.AppName))
}

type serverRecord struct {
	Host     string `json:"host" jsonschema:"server hostname or IP"`
	User     string `json:"user" jsonschema:"SSH user"`
	SSHKey   string `json:"ssh_key,omitempty" jsonschema:"private key path"`
	Password string `json:"password,omitempty" jsonschema:"SSH password (stored with 0600, never logged)"`
}

type teardownInput struct {
	AppName    string `json:"app_name" jsonschema:"application name to remove"`
	ConfigPath string `json:"config_path,omitempty" jsonschema:"path to easydrop.toml"`
}

func handleTeardown(ctx context.Context, _ *sdk.CallToolRequest, in teardownInput) (*sdk.CallToolResult, textOut, error) {
	if strings.TrimSpace(in.AppName) == "" {
		return errResult("app_name is required")
	}
	path := strings.TrimSpace(in.ConfigPath)
	if path == "" {
		path = defaultConfigPath
	}
	if err := deploy.Teardown(ctx, path, in.AppName); err != nil {
		return errResult(fmt.Sprintf("teardown failed: %v", err))
	}
	return okResult(fmt.Sprintf("%s torn down (volumes kept)", in.AppName))
}

type manageInput struct {
	Action string       `json:"action" jsonschema:"add or remove"`
	Config serverRecord `json:"config" jsonschema:"server record"`
}

func handleManageServer(_ context.Context, _ *sdk.CallToolRequest, in manageInput) (*sdk.CallToolResult, textOut, error) {
	msg, err := ManageServer(in.Action, in.Config.Host, in.Config.User, in.Config.SSHKey, in.Config.Password)
	if err != nil {
		return errResult(fmt.Sprintf("manage_server failed: %v", err))
	}
	return okResult(msg)
}

// mcpErr builds an IsError result for handlers with a custom output type.
func mcpErr[Out any](msg string) (*sdk.CallToolResult, Out, error) {
	var zero Out
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: msg}},
		IsError: true,
	}, zero, nil
}
