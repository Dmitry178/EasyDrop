package core

import (
	"testing"

	"easydrop/internal/models"
)

func TestNewExecutorLocalhost(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1"} {
		ex, err := NewExecutor(&models.ServerConfig{Host: host, User: "root"})
		if err != nil {
			t.Fatalf("NewExecutor(%q) unexpected error: %v", host, err)
		}
		if _, ok := ex.(*LocalExecutor); !ok {
			t.Errorf("NewExecutor(%q) = %T, want *LocalExecutor", host, ex)
		}
		ex.Close()
	}
}

func TestNewExecutorRemote(t *testing.T) {
	ex, err := NewExecutor(&models.ServerConfig{
		Host: "203.0.113.10", User: "deploy", SSHKey: "~/.ssh/id_rsa",
	})
	if err != nil {
		t.Fatalf("NewExecutor() unexpected error: %v", err)
	}
	sshEx, ok := ex.(*SSHExecutor)
	if !ok {
		t.Fatalf("NewExecutor() = %T, want *SSHExecutor", ex)
	}
	if sshEx.addr() != "203.0.113.10:22" {
		t.Errorf("addr() = %q, want %q", sshEx.addr(), "203.0.113.10:22")
	}
	// Lazy: constructing must not dial — Close on a never-connected
	// executor must succeed.
	if err := ex.Close(); err != nil {
		t.Errorf("Close() on idle executor = %v, want nil", err)
	}
}

func TestNewExecutorRemoteCustomPort(t *testing.T) {
	ex, err := NewExecutor(&models.ServerConfig{Host: "example.com", User: "u", Port: 2222})
	if err != nil {
		t.Fatalf("NewExecutor() unexpected error: %v", err)
	}
	defer ex.Close()
	if sshEx, ok := ex.(*SSHExecutor); !ok || sshEx.addr() != "example.com:2222" {
		t.Errorf("expected SSHExecutor to example.com:2222, got %T", ex)
	}
}

func TestNewExecutorNilConfig(t *testing.T) {
	if _, err := NewExecutor(nil); err == nil {
		t.Errorf("NewExecutor(nil) expected error, got nil")
	}
}

func TestNewExecutorRemoteMissingUser(t *testing.T) {
	if _, err := NewExecutor(&models.ServerConfig{Host: "example.com"}); err == nil {
		t.Errorf("NewExecutor() without user expected error, got nil")
	}
}

func TestEscapeShellArg(t *testing.T) {
	cases := map[string]string{
		`simple`:      `'simple'`,
		`a;b`:         `'a;b'`,
		`it's`:        `'it'"'"'s'`,
		`$(evil)`:     `'$(evil)'`,
		`my-proj.com`: `'my-proj.com'`,
	}
	for in, want := range cases {
		if got := EscapeShellArg(in); got != want {
			t.Errorf("EscapeShellArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToRemotePath(t *testing.T) {
	if got := toRemotePath(`C:\tmp\file`); got != "C:/tmp/file" {
		t.Errorf("toRemotePath() = %q", got)
	}
	if got := toRemotePath("/tmp//easydrop/"); got != "/tmp/easydrop" {
		t.Errorf("toRemotePath() = %q", got)
	}
}
