package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"easydrop/internal/models"
)

// defaultAppPort is used when no EXPOSE directive is found.
const defaultAppPort = 8080

var exposePattern = regexp.MustCompile(`(?i)^\s*EXPOSE\s+(\d+)`)

// Scaffold scans dir (Dockerfile, compose files, go.mod, package.json) and
// builds a pre-filled Config. It never touches the filesystem.
func Scaffold(dir string) (*models.Config, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve dir %q: %w", dir, err)
	}
	cfg := &models.Config{
		App: models.AppConfig{
			Name:            sanitizeAppName(filepath.Base(abs)),
			Port:            detectPort(abs),
			HealthCheckPath: "/",
		},
		Server: models.ServerConfig{
			Host:   "localhost",
			User:   currentUser(),
			SSHKey: "~/.ssh/id_rsa",
			Port:   22,
		},
		Build: models.BuildConfig{Strategy: "remote"},
		Driver: models.DriverConfig{
			Type: detectDriver(abs),
		},
		Nginx: models.NginxConfig{SSL: false},
	}
	if cfg.Driver.Type == "compose" {
		cfg.Driver.ComposeFile = "docker-compose.yml"
		if _, err := os.Stat(filepath.Join(abs, "docker-compose.yaml")); err == nil {
			cfg.Driver.ComposeFile = "docker-compose.yaml"
		}
	}
	if err := ApplyDefaults(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// WriteConfig marshals cfg to path. Without force it refuses to overwrite.
func WriteConfig(path string, cfg *models.Config, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config %q already exists (use --force to overwrite)", path)
		}
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	return nil
}

// detectDriver prefers compose when a compose file is present.
func detectDriver(dir string) string {
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return "compose"
		}
	}
	return "single"
}

// detectPort reads the first EXPOSE port from the Dockerfile, if any.
func detectPort(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		return defaultAppPort
	}
	for _, line := range strings.Split(string(data), "\n") {
		if m := exposePattern.FindStringSubmatch(line); m != nil {
			if port, err := strconv.Atoi(m[1]); err == nil && port >= 1 && port <= 65535 {
				return port
			}
		}
	}
	return defaultAppPort
}

// sanitizeAppName lowercases the directory base and replaces anything
// outside the docker-compatible set with dashes.
func sanitizeAppName(base string) string {
	lower := strings.ToLower(base)
	var sb strings.Builder
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	name := strings.Trim(sb.String(), ".-")
	if name == "" {
		return "app"
	}
	return name
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "root"
}
