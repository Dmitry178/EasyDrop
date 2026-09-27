package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalExecutor runs commands on the local machine.
// It satisfies CommandExecutor; Close is a no-op.
type LocalExecutor struct{}

// NewLocalExecutor creates an executor targeting the local environment.
func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}

// ExecCommand routes the command through `sh -c` with the given context,
// capturing stdout, stderr and the exit code. A non-zero exit yields
// both exitCode != 0 and a non-nil error.
func (e *LocalExecutor) ExecCommand(ctx context.Context, cmd string) (string, string, int, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	var stdoutBuf, stderrBuf bytes.Buffer
	c.Stdout = &stdoutBuf
	c.Stderr = &stderrBuf

	err := c.Run()
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			return stdout, stderr, code, fmt.Errorf("local command exited with code %d: %s", code, strings.TrimSpace(stderr))
		}
		// Context cancellation, binary missing, etc.
		return stdout, stderr, -1, fmt.Errorf("local command failed: %w", err)
	}
	return stdout, stderr, 0, nil
}

// UploadFile copies srcPath to destPath on the local filesystem,
// creating parent directories and setting 0644 permissions.
func (e *LocalExecutor) UploadFile(_ context.Context, srcPath, destPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read source file %q: %w", srcPath, err)
	}
	if dir := filepath.Dir(destPath); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create parent dirs for %q: %w", destPath, err)
		}
	}
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("write dest file %q: %w", destPath, err)
	}
	return nil
}

// Close releases resources. No-op for the local executor.
func (e *LocalExecutor) Close() error {
	return nil
}
