package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"easydrop/internal/models"
)

// ParseConfig reads, parses and validates the easydrop.toml at the given path.
// It applies fallback defaults for optional properties.
func ParseConfig(path string) (*models.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg models.Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	// --- Required fields ---
	if strings.TrimSpace(cfg.App.Name) == "" {
		return nil, fmt.Errorf("validation error: app.name is required")
	}
	if strings.TrimSpace(cfg.Server.Host) == "" {
		return nil, fmt.Errorf("validation error: server.host is required")
	}

	// --- Defaults ---
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 22
	}
	if strings.TrimSpace(cfg.Server.SSHKey) == "" {
		cfg.Server.SSHKey = "~/.ssh/id_rsa"
	}
	expanded, err := expandTilde(cfg.Server.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("expand server.ssh_key: %w", err)
	}
	cfg.Server.SSHKey = expanded

	if strings.TrimSpace(cfg.Build.Strategy) == "" {
		cfg.Build.Strategy = "remote"
	}
	if cfg.Build.Strategy != "remote" && cfg.Build.Strategy != "local" {
		return nil, fmt.Errorf("validation error: build.strategy must be \"remote\" or \"local\", got %q", cfg.Build.Strategy)
	}

	if strings.TrimSpace(cfg.Driver.Type) == "" {
		cfg.Driver.Type = "solo"
	}
	if cfg.Driver.Type != "solo" && cfg.Driver.Type != "compose" && cfg.Driver.Type != "swarm" {
		return nil, fmt.Errorf("validation error: driver.type must be \"solo\", \"compose\" or \"swarm\", got %q", cfg.Driver.Type)
	}
	if cfg.Driver.Type == "compose" && strings.TrimSpace(cfg.Driver.ComposeFile) == "" {
		cfg.Driver.ComposeFile = "docker-compose.yml"
	}

	if strings.TrimSpace(cfg.App.HealthCheckPath) == "" {
		cfg.App.HealthCheckPath = "/"
	}

	return &cfg, nil
}

// expandTilde expands a leading "~" to the current user's home directory.
func expandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	// "~user/..." form is not supported — return as-is with error context
	return "", fmt.Errorf("unsupported tilde expansion in path %q (only ~/ is supported)", path)
}
