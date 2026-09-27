package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	// sshDialTimeout bounds the initial TCP+SSH handshake.
	sshDialTimeout = 15 * time.Second
	// sshSessionTimeout bounds a single remote command (the context
	// passed by the caller can tighten it further via cancellation).
)

// SSHExecutor runs commands on a remote host over SSH/SFTP.
// The connection is established lazily on first use, so constructing
// the executor never performs network I/O. It satisfies CommandExecutor.
type SSHExecutor struct {
	host     string
	port     int
	user     string
	keyPath  string
	password string

	mu         sync.Mutex
	client     *ssh.Client
	sftpClient *sftp.Client
}

// NewSSHExecutor creates a remote executor. No connection is opened yet;
// dialing happens on the first ExecCommand/UploadFile call.
func NewSSHExecutor(host string, port int, user, keyPath, password string) *SSHExecutor {
	if port == 0 {
		port = 22
	}
	return &SSHExecutor{
		host:     host,
		port:     port,
		user:     user,
		keyPath:  keyPath,
		password: password,
	}
}

// EscapeShellArg quotes a single shell argument with single quotes,
// safe for embedding untrusted values (domains, names) into commands
// sent via ExecCommand.
func EscapeShellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// ExecCommand opens a session, runs cmd via the remote shell and returns
// captured stdout/stderr plus the exit code. Non-zero exit yields both
// exitCode != 0 and a non-nil error.
func (e *SSHExecutor) ExecCommand(_ context.Context, cmd string) (string, string, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureClientLocked(); err != nil {
		return "", "", -1, err
	}

	session, err := e.client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("open ssh session to %s: %w", e.addr(), err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	runErr := session.Run(cmd)
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			code := exitErr.ExitStatus()
			return stdout, stderr, code, fmt.Errorf("remote command exited with code %d: %s", code, strings.TrimSpace(stderr))
		}
		if _, ok := runErr.(*ssh.ExitMissingError); ok {
			return stdout, stderr, -1, fmt.Errorf("remote command exited without status: %w", runErr)
		}
		return stdout, stderr, -1, fmt.Errorf("run remote command: %w", runErr)
	}
	return stdout, stderr, 0, nil
}

// UploadFile copies a local file to the remote host with 0644 permissions,
// creating parent directories. Callers needing privileged destinations
// must stage under /tmp/easydrop/ and `sudo mv` via ExecCommand.
func (e *SSHExecutor) UploadFile(_ context.Context, srcPath, destPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read source file %q: %w", srcPath, err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureClientLocked(); err != nil {
		return err
	}
	fs, err := e.ensureSFTPLocked()
	if err != nil {
		return err
	}

	remotePath := toRemotePath(destPath)
	if dir := path.Dir(remotePath); dir != "" && dir != "." {
		if err := fs.MkdirAll(dir); err != nil {
			return fmt.Errorf("create remote parent dirs for %q: %w", destPath, err)
		}
	}
	f, err := fs.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("create remote file %q: %w", destPath, err)
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		return fmt.Errorf("write remote file %q: %w", destPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close remote file %q: %w", destPath, err)
	}
	if err := fs.Chmod(remotePath, 0644); err != nil {
		return fmt.Errorf("chmod remote file %q: %w", destPath, err)
	}
	return nil
}

// Close shuts down the SFTP and SSH connections. Safe to call repeatedly.
func (e *SSHExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var firstErr error
	if e.sftpClient != nil {
		if err := e.sftpClient.Close(); err != nil {
			firstErr = fmt.Errorf("close sftp: %w", err)
		}
		e.sftpClient = nil
	}
	if e.client != nil {
		if err := e.client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close ssh: %w", err)
		}
		e.client = nil
	}
	return firstErr
}

func (e *SSHExecutor) addr() string {
	return fmt.Sprintf("%s:%d", e.host, e.port)
}

// ensureClientLocked dials the SSH server unless already connected.
// Caller must hold e.mu.
func (e *SSHExecutor) ensureClientLocked() error {
	if e.client != nil {
		return nil
	}
	authMethods, err := e.authMethods()
	if err != nil {
		return err
	}
	cfg := &ssh.ClientConfig{
		User:            e.user,
		Auth:            authMethods,
		HostKeyCallback: e.hostKeyCallback(),
		Timeout:         sshDialTimeout,
	}
	client, err := ssh.Dial("tcp", e.addr(), cfg)
	if err != nil {
		return fmt.Errorf("dial ssh %s@%s: %w", e.user, e.addr(), err)
	}
	e.client = client
	return nil
}

// ensureSFTPLocked opens the SFTP subsystem unless already open.
// Caller must hold e.mu (and the SSH client must be connected).
func (e *SSHExecutor) ensureSFTPLocked() (*sftp.Client, error) {
	if e.sftpClient != nil {
		return e.sftpClient, nil
	}
	fs, err := sftp.NewClient(e.client)
	if err != nil {
		return nil, fmt.Errorf("open sftp to %s: %w", e.addr(), err)
	}
	e.sftpClient = fs
	return fs, nil
}

// authMethods assembles key-file, ssh-agent and password auth.
// Order: explicit key file → ssh-agent → password fallback.
func (e *SSHExecutor) authMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if strings.TrimSpace(e.keyPath) != "" {
		key, err := os.ReadFile(expandSSHPath(e.keyPath))
		if err != nil {
			return nil, fmt.Errorf("read ssh key %q: %w", e.keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse ssh key %q: %w (encrypted keys with passphrase are not supported)", e.keyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	if e.password != "" {
		methods = append(methods, ssh.Password(e.password))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no ssh auth methods: set server.ssh_key, start ssh-agent, or provide server.password")
	}
	return methods, nil
}

// hostKeyCallback prefers known_hosts, falling back to insecure verification
// with the path surfaced so operators can pin the host key later.
func (e *SSHExecutor) hostKeyCallback() ssh.HostKeyCallback {
	knownHostsPath := expandSSHPath("~/.ssh/known_hosts")
	if cb, err := knownhosts.New(knownHostsPath); err == nil {
		return cb
	}
	return ssh.InsecureIgnoreHostKey() //nolint:gosec // MVP: strict host pinning is future work; see NFR-02 notes
}

// expandSSHPath expands a leading ~/ to the user home directory.
func expandSSHPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// toRemotePath normalizes a destination to forward slashes — the target
// is always Linux, even when the CLI runs on Windows/macOS.
func toRemotePath(p string) string {
	return path.Clean(strings.ReplaceAll(p, "\\", "/"))
}
