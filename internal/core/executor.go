package core

import "context"

// CommandExecutor abstracts local vs remote (SSH) host operations.
//
// Contract:
//   - ExecCommand runs cmd via the host shell and captures stdout/stderr.
//     A non-zero exit code is returned as BOTH exitCode != 0 AND a non-nil err,
//     so callers can detect missing binaries with `if err != nil` (e.g.
//     Bootstrapper's `docker --version` probe).
//   - UploadFile copies a local file to destPath on the target host with 0644
//     permissions, creating parent directories as needed. Privileged
//     destinations (e.g. /etc/nginx) must NOT be written directly — stage to
//     /tmp/easydrop/ and move atomically with `sudo mv` via ExecCommand.
//   - Close releases underlying connections (SSH/SFTP). LocalExecutor's
//     Close is a no-op returning nil. Callers must call Close when done.
//
// Implementations: LocalExecutor (local_client.go), SSHExecutor (ssh_client.go).
// Use NewExecutor (factory.go) to pick one based on ServerConfig.Host.
type CommandExecutor interface {
	ExecCommand(ctx context.Context, cmd string) (stdout, stderr string, exitCode int, err error)
	UploadFile(ctx context.Context, srcPath, destPath string) error
	Close() error
}
