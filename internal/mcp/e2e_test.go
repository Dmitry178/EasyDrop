package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// e2eClient spins up the real server with an in-memory transport pair and a
// real SDK client — the same wire both sides speak over stdio, without
// depending on process pipes.
func e2eClient(t *testing.T) *sdk.ClientSession {
	t.Helper()
	srvT, cliT := sdk.NewInMemoryTransports()
	sctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = NewServer().Run(sctx, srvT)
	}()
	cli := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "v0"}, nil)
	sess, err := cli.Connect(context.Background(), cliT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func callTool(t *testing.T, sess *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func toolText(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("empty tool content: %+v", res)
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("first content is %T, want *TextContent", res.Content[0])
	}
	return tc.Text
}

func TestE2EToolsList(t *testing.T) {
	sess := e2eClient(t)
	res, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	for _, want := range []string{"init_project", "deploy_app", "get_status", "get_logs", "rollback_app", "manage_server"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("tool %q missing, got %v", want, names)
		}
	}
}

func TestE2EInitAndResources(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM x\nEXPOSE 3000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	sess := e2eClient(t)

	res := callTool(t, sess, "init_project", map[string]any{"force": true})
	if res.IsError {
		t.Fatalf("init_project failed: %s", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "3000") {
		t.Errorf("init must detect EXPOSE port: %s", toolText(t, res))
	}

	lr, err := sess.ListResources(context.Background(), &sdk.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(lr.Resources) != 2 {
		t.Fatalf("want 2 resources, got %v", lr.Resources)
	}
	rr, err := sess.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "easydrop://docs/schema"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(rr.Contents) != 1 || !strings.Contains(rr.Contents[0].Text, `"blue_green"`) {
		t.Errorf("schema resource must describe the config")
	}
}

func TestE2EManageServer(t *testing.T) {
	t.Setenv("EASYDROP_SERVERS_FILE", filepath.Join(t.TempDir(), "servers.toml"))
	sess := e2eClient(t)
	res := callTool(t, sess, "manage_server", map[string]any{
		"action": "add",
		"config": map[string]any{"host": "203.0.113.10", "user": "deploy", "password": "s3cret"},
	})
	if res.IsError {
		t.Fatalf("add failed: %s", toolText(t, res))
	}
	if strings.Contains(toolText(t, res), "s3cret") {
		t.Errorf("secrets must never appear in output: %q", toolText(t, res))
	}
	bad := callTool(t, sess, "manage_server", map[string]any{
		"action": "explode", "config": map[string]any{"host": "h", "user": "u"},
	})
	if !bad.IsError {
		t.Errorf("invalid action must yield IsError")
	}
}

func TestE2EStatusWithoutConfigIsError(t *testing.T) {
	t.Chdir(t.TempDir()) // no easydrop.toml
	sess := e2eClient(t)
	res := callTool(t, sess, "get_status", map[string]any{"app_name": "my-api"})
	if !res.IsError {
		t.Errorf("missing config must yield IsError, got %s", toolText(t, res))
	}
}
