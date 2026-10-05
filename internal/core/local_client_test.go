package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalExecutorEcho(t *testing.T) {
	ex := NewLocalExecutor()
	defer ex.Close()

	stdout, _, code, err := ex.ExecCommand(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("ExecCommand() unexpected error: %v", err)
	}
	if code != 0 {
		t.Fatalf("ExecCommand() exit code = %d, want 0", code)
	}
	if strings.TrimSpace(stdout) != "hello" {
		t.Errorf("ExecCommand() stdout = %q, want %q", stdout, "hello")
	}
}

func TestLocalExecutorNonZeroExit(t *testing.T) {
	ex := NewLocalExecutor()
	defer ex.Close()

	_, _, code, err := ex.ExecCommand(context.Background(), "exit 3")
	if err == nil {
		t.Fatalf("ExecCommand() expected error for exit 3, got nil")
	}
	if code != 3 {
		t.Errorf("ExecCommand() exit code = %d, want 3", code)
	}
}

func TestLocalExecutorContextCancel(t *testing.T) {
	ex := NewLocalExecutor()
	defer ex.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := ex.ExecCommand(ctx, "sleep 10")
	if err == nil {
		t.Errorf("ExecCommand() expected error for cancelled context, got nil")
	}
}

func TestLocalExecutorUploadFile(t *testing.T) {
	ex := NewLocalExecutor()
	defer ex.Close()

	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("payload"), 0600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	dst := filepath.Join(dir, "nested", "dst.txt")

	if err := ex.UploadFile(context.Background(), src, dst); err != nil {
		t.Fatalf("UploadFile() unexpected error: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("dst content = %q, want %q", data, "payload")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if runtime.GOOS == "windows" {
		// Go reports 0666 on Windows for every regular file; there are no POSIX
		// mode bits to check.
		t.Logf("dst mode on Windows = %o", info.Mode().Perm())
	} else if info.Mode().Perm() != 0644 {
		t.Errorf("dst perm = %o, want 644", info.Mode().Perm())
	}
}

func TestLocalExecutorUploadMissingSrc(t *testing.T) {
	ex := NewLocalExecutor()
	defer ex.Close()

	err := ex.UploadFile(context.Background(), "/nonexistent/src.txt", filepath.Join(t.TempDir(), "dst.txt"))
	if err == nil {
		t.Errorf("UploadFile() expected error for missing src, got nil")
	}
}
