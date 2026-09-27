package core

import (
	"easydrop/internal/models"
	"fmt"
)

// NewExecutor picks a CommandExecutor for the target environment:
// LocalExecutor for localhost/127.0.0.1, SSHExecutor otherwise.
// The SSH connection is opened lazily on first use; the caller owns
// the returned executor and must call Close when done.
func NewExecutor(cfg *models.ServerConfig) (CommandExecutor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("server config is nil")
	}
	if cfg.Host == "localhost" || cfg.Host == "127.0.0.1" {
		return NewLocalExecutor(), nil
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("server.user is required for remote host %q", cfg.Host)
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	return NewSSHExecutor(cfg.Host, port, cfg.User, cfg.SSHKey, cfg.Password), nil
}
