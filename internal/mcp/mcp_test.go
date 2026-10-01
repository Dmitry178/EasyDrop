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
	t.Setenv("EASYDROP_VAULT_PASSWORD", "test-vault-password")
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
	// EASYDROP_SERVERS_FILE points at legacy .toml; the vault is its sibling.
	path := os.Getenv("EASYDROP_SERVERS_FILE")
	vault := path[:len(path)-len(".toml")] + ".vault"
	info, err := os.Stat(vault)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("vault perm = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("no plaintext file must remain, stat err = %v", err)
	}
}

func TestStoreRequiresPassword(t *testing.T) {
	t.Setenv("EASYDROP_SERVERS_FILE", filepath.Join(t.TempDir(), "servers.toml"))
	os.Unsetenv("EASYDROP_VAULT_PASSWORD")
	if _, err := ManageServer("add", "h", "u", "", ""); err == nil {
		t.Errorf("vault without password must fail closed")
	} else if !strings.Contains(err.Error(), "EASYDROP_VAULT_PASSWORD") {
		t.Errorf("error must name the env var, got: %v", err)
	}
}

func TestStoreWrongPasswordFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EASYDROP_SERVERS_FILE", filepath.Join(dir, "servers.toml"))
	t.Setenv("EASYDROP_VAULT_PASSWORD", "correct")
	if _, err := ManageServer("add", "h", "u", "", "pw"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EASYDROP_VAULT_PASSWORD", "wrong")
	if _, err := ManageServer("remove", "h", "u", "", ""); err == nil {
		t.Errorf("wrong password must fail to open the vault")
	}
}

func TestStoreMigratesLegacyTOML(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "servers.toml")
	legacyContent := "[[servers]]\nhost = 'old.example.com'\nuser = 'root'\nport = 22\n"
	if err := os.WriteFile(legacy, []byte(legacyContent), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EASYDROP_SERVERS_FILE", legacy)
	t.Setenv("EASYDROP_VAULT_PASSWORD", "migrate-me")
	msg, err := ManageServer("remove", "old.example.com", "root", "", "")
	if err != nil {
		t.Fatalf("migrated record must be usable: %v", err)
	}
	if !strings.Contains(msg, "removed") {
		t.Errorf("unexpected message %q", msg)
	}
	if _, err := os.Stat(legacy + ".migrated"); err != nil {
		t.Errorf("legacy file must be retired aside, not deleted: %v", err)
	}
	vault := legacy[:len(legacy)-len(".toml")] + ".vault"
	blob, err := os.ReadFile(vault)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "old.example.com") {
		t.Errorf("vault payload must be encrypted, host leaked in cleartext")
	}
}

func TestServerRegistersWithoutPanic(t *testing.T) {
	// AddTool panics on bad schemas – construction alone validates them.
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

func TestHandleTeardownValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	res, _, _ := handleTeardown(context.Background(), &sdk.CallToolRequest{}, teardownInput{})
	if res == nil || !res.IsError {
		t.Errorf("empty app_name must yield IsError result")
	}
}
