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
// It resolves ${VAR} references in the string fields, applies fallback defaults
// for optional properties, and expands server.ssh_key.
func ParseConfig(path string) (*models.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg models.Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	// Secrets first: the expansion may fill server.password / server.ssh_key,
	// and the required-field checks below should report on final values.
	// Resolution happens before ApplyDefaults so that a ${VAR:-~/.ssh/id_rsa}
	// default still gets its "~" expanded afterwards.
	if err := resolveSecrets(&cfg, filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("config %q: %w", path, err)
	}

	// --- Required fields ---
	if strings.TrimSpace(cfg.App.Name) == "" {
		return nil, fmt.Errorf("validation error: app.name is required")
	}
	if strings.TrimSpace(cfg.Server.Host) == "" {
		return nil, fmt.Errorf("validation error: server.host is required")
	}

	if err := ApplyDefaults(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ApplyDefaults fills fallback defaults and expands server.ssh_key.
// Exported for the init scaffolding, which builds Config in memory.
//
// A zero app.port is rejected here rather than defaulted: `init` deliberately
// leaves the key out when it cannot detect a port (OD-04), and a missing value
// has to stop the deploy instead of silently becoming 8080 – a wrong port does
// not fail the build, it fails the healthcheck for `single` and produces a 502
// for compose/swarm, which have no HTTP probe at all.
func ApplyDefaults(cfg *models.Config) error {
	if err := applyNonPortDefaults(cfg); err != nil {
		return err
	}
	if cfg.App.Port == 0 {
		return fmt.Errorf("validation error: app.port is required: set the port your app " +
			"listens on INSIDE the container (easydrop init omits it when it cannot detect " +
			"one). A wrong value does not fail the build – it fails the deploy healthcheck " +
			"for the single driver, and yields a 502 from the proxy for compose/swarm")
	}
	if err := validatePort(cfg.App.Port, "app.port"); err != nil {
		return err
	}
	if cfg.App.HostPort == 0 {
		cfg.App.HostPort = cfg.App.Port
	}
	if err := validatePort(cfg.App.HostPort, "app.host_port"); err != nil {
		return err
	}
	if cfg.App.HostPort == 65535 {
		return fmt.Errorf("validation error: app.host_port 65535 leaves no port for the Blue-Green pair")
	}

	return nil
}

// applyNonPortDefaults fills every default except the port pair. init uses it
// to build a config that has no port yet: defaulting host_port to an absent
// port would fabricate a value the user never chose.
func applyNonPortDefaults(cfg *models.Config) error {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 22
	}
	if strings.TrimSpace(cfg.Server.SSHKey) == "" {
		cfg.Server.SSHKey = "~/.ssh/id_rsa"
	}
	expanded, err := expandTilde(cfg.Server.SSHKey)
	if err != nil {
		return fmt.Errorf("expand server.ssh_key: %w", err)
	}
	cfg.Server.SSHKey = expanded

	if strings.TrimSpace(cfg.Build.Strategy) == "" {
		cfg.Build.Strategy = "remote"
	}
	if cfg.Build.Strategy != "remote" && cfg.Build.Strategy != "local" {
		return fmt.Errorf("validation error: build.strategy must be \"remote\" or \"local\", got %q", cfg.Build.Strategy)
	}

	if strings.TrimSpace(cfg.Driver.Type) == "" {
		cfg.Driver.Type = "single"
	}
	if cfg.Driver.Type != "single" && cfg.Driver.Type != "compose" && cfg.Driver.Type != "swarm" {
		return fmt.Errorf("validation error: driver.type must be \"single\", \"compose\" or \"swarm\", got %q", cfg.Driver.Type)
	}
	if cfg.Driver.Type == "compose" && strings.TrimSpace(cfg.Driver.ComposeFile) == "" {
		cfg.Driver.ComposeFile = "docker-compose.yml"
	}

	if strings.TrimSpace(cfg.App.HealthCheckPath) == "" {
		cfg.App.HealthCheckPath = "/"
	}
	return nil
}

func validatePort(port int, field string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("validation error: %s must be 1-65535, got %d", field, port)
	}
	return nil
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
	// "~user/..." form is not supported – return as-is with error context
	return "", fmt.Errorf("unsupported tilde expansion in path %q (only ~/ is supported)", path)
}
