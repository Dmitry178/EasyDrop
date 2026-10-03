package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"easydrop/internal/models"
)

func TestAuthMethodsPasswordOnly(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	e := &SSHExecutor{password: "pw"}
	methods, err := e.authMethods()
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}
	if len(methods) != 1 {
		t.Errorf("want the password method only, got %d", len(methods))
	}
}

func TestAuthMethodsKeyFileWinsOverAgent(t *testing.T) {
	// An explicit key file is the first method regardless of the environment,
	// and an absent default key must not break a config that pins ssh_key.
	t.Setenv("SSH_AUTH_SOCK", "")
	e := &SSHExecutor{keyPath: "/nonexistent/id_ed25519"}
	if _, err := e.authMethods(); err == nil || !strings.Contains(err.Error(), "read ssh key") {
		t.Errorf("missing key file must be named, got: %v", err)
	}

	key := writeTestKey(t)
	t.Setenv("SSH_AUTH_SOCK", "/nonexistent/agent.sock")
	e = &SSHExecutor{keyPath: key, password: "pw"}
	methods, err := e.authMethods()
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}
	// key + agent is skipped (socket dead) + password = 2 usable methods.
	if len(methods) != 2 {
		t.Errorf("want key + password, got %d methods", len(methods))
	}
}

func TestAuthMethodsUnreachableAgentIsExplained(t *testing.T) {
	// The MCP-client case: the variable is inherited but the agent behind it is
	// gone. Reporting "no ssh auth methods" here sends the user hunting in the
	// wrong place.
	t.Setenv("SSH_AUTH_SOCK", "/nonexistent/agent.sock")
	e := &SSHExecutor{}
	_, err := e.authMethods()
	if err == nil {
		t.Fatal("expected an error with no usable method")
	}
	for _, want := range []string{"SSH_AUTH_SOCK", "unreachable", "MCP client"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q, got: %v", want, err)
		}
	}
}

func TestAuthMethodsNoCredentials(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	e := &SSHExecutor{}
	_, err := e.authMethods()
	if err == nil {
		t.Fatal("expected an error with no credentials at all")
	}
	for _, want := range []string{"no ssh auth methods", "server.ssh_key", "server.password"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q, got: %v", want, err)
		}
	}
	// Without a dead socket there is nothing to explain, so the MCP-client
	// advice must not leak into the plain message.
	if strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Errorf("unrelated advice in the generic error: %v", err)
	}
}

func TestAuthMethodsUnreachableAgentDoesNotBlockPassword(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/nonexistent/agent.sock")
	e := &SSHExecutor{password: "pw"}
	methods, err := e.authMethods()
	if err != nil {
		t.Fatalf("a dead agent socket must not fail the whole auth setup: %v", err)
	}
	if len(methods) != 1 {
		t.Errorf("want the password fallback, got %d methods", len(methods))
	}
}

// writeTestKey generates a throwaway unencrypted ed25519 key on disk and
// returns its path. Generated rather than committed: a private key in the
// repository is a private key in the repository, whatever its intent.
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "easydrop-test")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func TestNewExecutorPassesCredentials(t *testing.T) {
	// Guards the wiring the factory test above does not reach: a password in
	// the config must actually reach the executor.
	t.Setenv("SSH_AUTH_SOCK", "")
	ex, err := NewExecutor(&models.ServerConfig{
		Host: "203.0.113.10", User: "deploy", Password: "pw",
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	defer ex.Close()
	e, ok := ex.(*SSHExecutor)
	if !ok {
		t.Fatalf("NewExecutor() = %T, want *SSHExecutor", ex)
	}
	if _, err := e.authMethods(); err != nil {
		t.Errorf("password from the config must be usable: %v", err)
	}
}
