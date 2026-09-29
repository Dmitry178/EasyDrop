package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func tempStore(t *testing.T) {
	t.Helper()
	t.Setenv("EASYDROP_SERVERS_FILE", filepath.Join(t.TempDir(), "servers.toml"))
}

func TestStoreAddRemoveRoundTrip(t *testing.T) {
	tempStore(t)
	msg, err := ManageServer("add", "203.0.113.10", "deploy", "~/.ssh/id_rsa", "s3cret")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(msg, "203.0.113.10") || strings.Contains(msg, "s3cret") {
		t.Errorf("message must name host but never secrets, got %q", msg)
	}
	// Upsert same host+user replaces.
	if _, err := ManageServer("add", "203.0.113.10", "deploy", "~/.ssh/other", ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	msg, err = ManageServer("remove", "203.0.113.10", "deploy", "", "")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(msg, "removed") {
		t.Errorf("remove message: %q", msg)
	}
	if _, err := ManageServer("remove", "203.0.113.10", "deploy", "", ""); err == nil {
		t.Errorf("second remove must fail (nothing left)")
	}
}

func TestStoreValidation(t *testing.T) {
	tempStore(t)
	for _, tc := range []struct{ action, host, user string }{
		{"explode", "h", "u"},
		{"add", "", "u"},
		{"add", "h", ""},
	} {
		if _, err := ManageServer(tc.action, tc.host, tc.user, "", ""); err == nil {
			t.Errorf("ManageServer(%q,%q,%q) must fail", tc.action, tc.host, tc.user)
		}
	}
}

func TestStoreFilePerms(t *testing.T) {
	tempStore(t)
	if _, err := ManageServer("add", "h", "u", "", "pw"); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("EASYDROP_SERVERS_FILE")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("store perm = %o, want 600", info.Mode().Perm())
	}
}

func TestServerRegistersWithoutPanic(t *testing.T) {
	// AddTool panics on bad schemas — construction alone validates them.
	s := NewServer()
	if s == nil {
		t.Fatalf("NewServer() = nil")
	}
}

func TestHandleManageServerValidation(t *testing.T) {
	tempStore(t)
	res, _, _ := handleManageServer(context.Background(), &sdk.CallToolRequest{}, manageInput{Action: "nuke"})
	if res == nil || !res.IsError {
		t.Errorf("invalid action must yield IsError result")
	}
	res, out, _ := handleManageServer(context.Background(), &sdk.CallToolRequest{},
		manageInput{Action: "add", Config: serverRecord{Host: "h", User: "u"}})
	if res != nil && res.IsError {
		t.Fatalf("valid add must succeed: %+v", res)
	}
	if !strings.Contains(out.Message, "h") {
		t.Errorf("unexpected message %q", out.Message)
	}
}

func TestHandleStatusRequiresAppName(t *testing.T) {
	res, _, _ := handleStatus(context.Background(), &sdk.CallToolRequest{}, statusInput{})
	if res == nil || !res.IsError {
		t.Errorf("empty app_name must yield IsError result")
	}
}

func TestHandleLogsValidation(t *testing.T) {
	res, _, _ := handleLogs(context.Background(), &sdk.CallToolRequest{}, logsInput{AppName: "BAD NAME"})
	if res == nil || !res.IsError {
		t.Errorf("invalid app name must yield IsError result (no docker touched)")
	}
}

func TestHandleInitCreatesConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM x\nEXPOSE 3000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	res, out, _ := handleInit(context.Background(), &sdk.CallToolRequest{}, initInput{})
	if res != nil && res.IsError {
		t.Fatalf("init must succeed: %+v", res)
	}
	if !strings.Contains(out.Message, "3000") {
		t.Errorf("message should reflect detected port, got %q", out.Message)
	}
	if _, err := os.Stat(filepath.Join(dir, "easydrop.toml")); err != nil {
		t.Errorf("easydrop.toml must be created: %v", err)
	}
	// Second run without force must fail politely.
	res2, _, _ := handleInit(context.Background(), &sdk.CallToolRequest{}, initInput{})
	if res2 == nil || !res2.IsError {
		t.Errorf("second init without force must yield IsError result")
	}
}

func TestHandleRollbackWithoutConfig(t *testing.T) {
	t.Chdir(t.TempDir()) // no easydrop.toml here
	res, _, _ := handleRollback(context.Background(), &sdk.CallToolRequest{}, rollbackInput{AppName: "my-api"})
	if res == nil || !res.IsError {
		t.Errorf("rollback without config must yield IsError result (no docker touched)")
	}
}
