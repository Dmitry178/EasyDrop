package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// e2eClient spins up the real server with an in-memory transport pair and a
// real SDK client – the same wire both sides speak over stdio, without
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
	t.Setenv("EASYDROP_VAULT_PASSWORD", "e2e-password")
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

func TestE2ETeardownToolRegistered(t *testing.T) {
	sess := e2eClient(t)
	res, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := false
	for _, tl := range res.Tools {
		if tl.Name == "teardown_app" {
			found = true
		}
	}
	if !found {
		t.Error("teardown_app tool must be registered")
	}
}

func TestE2ETeardownWithoutConfigIsError(t *testing.T) {
	t.Chdir(t.TempDir()) // no easydrop.toml
	sess := e2eClient(t)
	res := callTool(t, sess, "teardown_app", map[string]any{"app_name": "my-api"})
	if !res.IsError {
		t.Errorf("teardown without config must yield IsError, got %s", toolText(t, res))
	}
}

func TestE2EToolAnnotations(t *testing.T) {
	sess := e2eClient(t)
	res, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*sdk.Tool{}
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}
	// Every tool must declare its annotations: the protocol defaults are
	// readOnly=false, which would misdescribe the read-only tools to a client
	// deciding what to call without asking the user.
	want := map[string]struct {
		readOnly, destructive, idempotent bool
	}{
		"init_project":  {false, true, true},
		"deploy_app":    {false, false, true},
		"get_status":    {true, false, false},
		"get_logs":      {true, false, false},
		"rollback_app":  {false, true, false},
		"teardown_app":  {false, true, true},
		"manage_server": {false, true, true},
	}
	for name, w := range want {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("tool %q missing", name)
			continue
		}
		if tl.Annotations == nil {
			t.Errorf("tool %q has no annotations", name)
			continue
		}
		a := tl.Annotations
		if a.ReadOnlyHint != w.readOnly {
			t.Errorf("%s ReadOnlyHint = %v, want %v", name, a.ReadOnlyHint, w.readOnly)
		}
		// DestructiveHint is meaningful only for the mutating tools (the spec
		// scopes it to ReadOnlyHint == false); a read-only tool must still
		// say ReadOnlyHint, and saying false there is meaningless noise.
		if !w.readOnly {
			if a.DestructiveHint == nil {
				t.Errorf("%s must state DestructiveHint explicitly (default is true)", name)
			} else if *a.DestructiveHint != w.destructive {
				t.Errorf("%s DestructiveHint = %v, want %v", name, *a.DestructiveHint, w.destructive)
			}
		} else if a.DestructiveHint != nil {
			t.Errorf("%s is read-only; DestructiveHint should be omitted", name)
		}
		if !w.readOnly && a.IdempotentHint != w.idempotent {
			t.Errorf("%s IdempotentHint = %v, want %v", name, a.IdempotentHint, w.idempotent)
		}
		if a.OpenWorldHint == nil {
			t.Errorf("%s must state OpenWorldHint explicitly", name)
		}
		if a.Title == "" {
			t.Errorf("%s must carry a display title", name)
		}
	}
	// A read-only tool must not advertise itself as touching the outside world.
	if a := byName["get_status"].Annotations; a.OpenWorldHint == nil || *a.OpenWorldHint {
		t.Errorf("get_status must declare a closed domain")
	}
}

func TestE2EDestructiveToolsAskForConfirmation(t *testing.T) {
	sess := e2eClient(t)
	res, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range res.Tools {
		if tl.Annotations == nil || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint {
			continue
		}
		// Descriptions are what the model actually reads; a destructive hint
		// alone does not stop it, so the description has to say so too.
		if !strings.Contains(strings.ToLower(tl.Description), "confirm") {
			t.Errorf("destructive tool %q must tell the agent to confirm with the user: %q", tl.Name, tl.Description)
		}
	}
}

func TestE2EGetLogsRejectsInvalidFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "easydrop.toml"), []byte("[app]\nname = \"my-api\"\nport = 8080\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	sess := e2eClient(t)
	// A bad expression must fail loudly rather than pass every line through,
	// which would look like an app that logs nothing but errors.
	res := callTool(t, sess, "get_logs", map[string]any{"app_name": "my-api", "filter": "error("})
	if !res.IsError {
		t.Errorf("invalid filter must be an error result, got: %s", toolText(t, res))
	}
	if !strings.Contains(toolText(t, res), "invalid log filter") {
		t.Errorf("error must name the bad filter: %s", toolText(t, res))
	}
}

func TestE2EGetLogsRequiresAppName(t *testing.T) {
	sess := e2eClient(t)
	res := callTool(t, sess, "get_logs", map[string]any{"app_name": "  "})
	if !res.IsError {
		t.Errorf("blank app_name must be rejected")
	}
}

func TestE2ETeardownRejectsBlankAppName(t *testing.T) {
	sess := e2eClient(t)
	res := callTool(t, sess, "teardown_app", map[string]any{"app_name": " "})
	if !res.IsError {
		t.Errorf("blank app_name must be rejected")
	}
}

// TestE2ETeardownAdvertisesIngressRemoval pins the tool description against the
// behaviour. `teardown_app` now deletes the nginx vhost, which is a materially
// bigger effect than "remove the deployment" – an agent that read the old
// wording would not think to confirm with the user before calling it.
// inputSchemaProperties extracts the property map from a tool's input schema,
// which the SDK hands back as an untyped value on the client side.
func inputSchemaProperties(t *testing.T, tl *sdk.Tool) map[string]any {
	t.Helper()
	raw, err := json.Marshal(tl.InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	var decoded struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode input schema: %v", err)
	}
	return decoded.Properties
}

func TestE2ETeardownAdvertisesIngressRemoval(t *testing.T) {
	sess := e2eClient(t)
	res, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range res.Tools {
		if tl.Name != "teardown_app" {
			continue
		}
		for _, want := range []string{"vhost", "purge", "certbot", "volumes"} {
			if !strings.Contains(tl.Description, want) {
				t.Errorf("teardown_app description must mention %q: %q", want, tl.Description)
			}
		}
		// The schema description is what the model reads for the argument itself.
		props := inputSchemaProperties(t, tl)
		raw, ok := props["purge"]
		if !ok {
			t.Fatal("teardown_app must expose a purge argument")
		}
		prop, _ := raw.(map[string]any)
		desc, _ := prop["description"].(string)
		if !strings.Contains(desc, "Let's Encrypt") {
			t.Errorf("purge must state that ACME certificates are never deleted, got %q", desc)
		}
		return
	}
	t.Fatal("teardown_app not found")
}
