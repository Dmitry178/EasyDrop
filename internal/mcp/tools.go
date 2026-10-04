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

// Log tail defaults. The unfiltered default is small because the raw lines go
// straight into the model's context. A filtered read is given a much wider
// window: the agent asking for "error|panic" wants the error to be *in* the
// scanned range, and a narrow window would return "0 matched", which reads as
// "the container is healthy" rather than "raise tail and look again".
const (
	defaultLogTail      = 50
	filteredLogTail     = 1000
	logOutputLineBudget = 400
)

type logsInput struct {
	AppName string `json:"app_name" jsonschema:"application name to read logs from"`
	Tail    int    `json:"tail,omitempty" jsonschema:"lines of history to scan, defaults to 50 (1000 when filter is set)"`
	Follow  bool   `json:"follow,omitempty" jsonschema:"keep streaming until the client cancels"`
	Filter  string `json:"filter,omitempty" jsonschema:"case-insensitive regular expression; only matching lines are returned, e.g. \"error|panic|fatal|traceback\". Use it to diagnose a failing container without flooding the context window"`
}

func handleLogs(ctx context.Context, _ *sdk.CallToolRequest, in logsInput) (*sdk.CallToolResult, logsOut, error) {
	if strings.TrimSpace(in.AppName) == "" {
		return mcpErr[logsOut]("app_name is required")
	}
	tail := in.Tail
	if tail <= 0 {
		tail = defaultLogTail
		if strings.TrimSpace(in.Filter) != "" {
			tail = filteredLogTail
		}
	}
	limit := 0
	if in.Follow {
		limit = followLogCap
	}
	res, err := deploy.Logs(ctx, defaultConfigPath, in.AppName, deploy.LogOptions{
		Lines: tail, Follow: in.Follow, Limit: limit, Filter: in.Filter,
	})
	if err != nil {
		return mcpErr[logsOut](fmt.Sprintf("logs failed: %v", err))
	}
	if len(res.Lines) > logOutputLineBudget {
		// A follow stream is unbounded by nature; the cap above can still hand
		// back more text than a model should ingest in one turn. State the
		// truncation instead of silently returning the first N lines.
		res.Lines = append(res.Lines[:logOutputLineBudget:logOutputLineBudget],
			fmt.Sprintf("[easydrop] output truncated: %d of %d matching lines shown – narrow `filter` or lower `tail`", logOutputLineBudget, res.Matched))
		res.Matched = len(res.Lines)
	}
	msg := strings.Join(res.Lines, "\n")
	if len(msg) == 0 {
		msg = noLogsMessage(res)
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: msg}},
	}, logsOut{
		Lines:   res.Lines,
		Scanned: res.Scanned,
		Matched: res.Matched,
		Filter:  strings.TrimSpace(in.Filter),
	}, nil
}

// noLogsMessage distinguishes the three empty cases an agent must not confuse:
// nothing logged at all, a filter that matched nothing, and a filter that was
// rejected. Only the first one means "the container is quiet".
func noLogsMessage(res *deploy.LogResult) string {
	if res.Scanned == 0 {
		return "[easydrop] no log lines available (container may not be running, or its logs are empty)"
	}
	return fmt.Sprintf("[easydrop] no lines matched (scanned %d lines – widen `tail` or loosen `filter`)", res.Scanned)
}

type logsOut struct {
	Lines   []string `json:"lines" jsonschema:"log lines that passed the filter, in order"`
	Scanned int      `json:"scanned" jsonschema:"lines produced by the container before filtering"`
	Matched int      `json:"matched" jsonschema:"number of lines returned (equal to len(lines))"`
	Filter  string   `json:"filter,omitempty" jsonschema:"the filter expression applied, if any"`
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
	Purge      bool   `json:"purge,omitempty" jsonschema:"also delete the self-signed certificate for nginx.domain. Off by default: it is re-created on the next deploy, but re-trusting it in a browser costs the user a click. A Let's Encrypt certificate is never deleted"`
}

func handleTeardown(ctx context.Context, _ *sdk.CallToolRequest, in teardownInput) (*sdk.CallToolResult, teardownOut, error) {
	if strings.TrimSpace(in.AppName) == "" {
		return mcpErr[teardownOut]("app_name is required")
	}
	path := strings.TrimSpace(in.ConfigPath)
	if path == "" {
		path = defaultConfigPath
	}
	res, err := deploy.Teardown(ctx, path, in.AppName, deploy.TeardownOptions{Purge: in.Purge})
	if err != nil {
		return mcpErr[teardownOut](fmt.Sprintf("teardown failed: %v", err))
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: teardownReport(res)}},
	}, teardownOut{
		AppName:         res.AppName,
		VolumesKept:     true,
		IngressRemoved:  res.IngressRemoved,
		IngressReloaded: res.IngressReloaded,
		CertPurged:      res.CertPurged,
		Note:            res.IngressNote,
	}, nil
}

// teardownReport is the single wording of a teardown outcome, shared by the CLI
// and this handler so an agent reads the same facts a human does (M15's rule,
// applied here).
func teardownReport(res *deploy.TeardownResult) string {
	msg := fmt.Sprintf("%s torn down (named volumes kept)", res.AppName)
	if res.IngressNote != "" {
		msg += "\n" + res.IngressNote
	}
	if res.CertPurged {
		msg += "\nself-signed certificate deleted; the next deploy issues a new one"
	}
	return msg
}

type teardownOut struct {
	AppName         string `json:"app_name" jsonschema:"resolved application name"`
	VolumesKept     bool   `json:"volumes_kept" jsonschema:"always true – easydrop never deletes named volumes"`
	IngressRemoved  bool   `json:"ingress_removed" jsonschema:"whether the managed nginx vhost was deleted"`
	IngressReloaded bool   `json:"ingress_reloaded" jsonschema:"whether nginx was reloaded; false means the old config is still live"`
	CertPurged      bool   `json:"cert_purged,omitempty" jsonschema:"whether a self-signed certificate was deleted (purge only)"`
	Note            string `json:"note,omitempty" jsonschema:"explanation when something was skipped or nginx could not be reloaded"`
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
