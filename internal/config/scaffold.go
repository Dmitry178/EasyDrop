package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"easydrop/internal/models"
)

// ScaffoldResult pairs the generated config with what was detected and where
// each value came from (M14). `init` must be able to say "port 3000, from
// package.json" – and, when nothing declared a port, that it left the key out
// instead of writing a guess (OD-04).
type ScaffoldResult struct {
	*models.Config
	// PortSource explains app.port; PortMissing reports that there is none.
	PortSource string
	// AppNameSource explains app.name.
	AppNameSource string
	// Stack is the detected primary language, or "" when unknown.
	Stack string
}

// PortMissing reports that nothing in the project stated the listen port, so
// `app.port` was left out of the generated config. Such a config is
// deliberately incomplete: the deploy fails with an actionable message instead
// of building fine and breaking later.
func (r *ScaffoldResult) PortMissing() bool {
	return r != nil && r.PortSource == PortSourceUnknown
}

// ScaffoldOptions overrides what detection decides.
type ScaffoldOptions struct {
	// Port, when non-zero, is used verbatim and outranks every detection
	// source. It is the supported escape hatch for the case detection cannot
	// cover (OD-04): the caller knows the port, or was told, and rather than
	// having it invented into the config. 0 means "detect".
	Port int
}

// Scaffold scans dir and builds a pre-filled Config, detecting everything.
func Scaffold(dir string) (*ScaffoldResult, error) {
	return ScaffoldWith(dir, ScaffoldOptions{})
}

// ScaffoldWith is Scaffold with explicit overrides. See Scaffold for what
// detection does and, importantly, for what it refuses to do.
//
// Detection (M14) reads the Dockerfile, the compose file, package.json, the
// language manifests and — as a bounded last resort — the project's own source
// for a literal listen port. It never touches anything outside dir and never
// contacts a host: no SSH, no Docker, no network.
//
// When no port is detected and none is supplied, `app.port` stays 0 and is
// omitted from the written file. It is NOT defaulted to 8080: see PortMissing.
func ScaffoldWith(dir string, opts ScaffoldOptions) (*ScaffoldResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve dir %q: %w", dir, err)
	}
	if opts.Port != 0 && !validPort(opts.Port) {
		return nil, fmt.Errorf("validation error: port must be 1-65535, got %d", opts.Port)
	}

	driver, composeFile := detectDriver(abs)
	port, portSource := detectPort(abs, driver, composeFile)
	if opts.Port != 0 {
		port, portSource = opts.Port, PortSourceOverride
	}
	name, nameSource := detectAppName(abs, filepath.Base(abs))

	cfg := &models.Config{
		App: models.AppConfig{
			Name:            name,
			Port:            port,
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
			Type:        driver,
			ComposeFile: composeFile,
		},
		Nginx: models.NginxConfig{SSL: false},
	}
	// Only the full defaulting when a port is known: host_port defaults to the
	// app port, so running it against an absent port would fabricate a second
	// value the user never chose.
	if port == 0 {
		err = applyNonPortDefaults(cfg)
	} else {
		err = ApplyDefaults(cfg)
	}
	if err != nil {
		return nil, err
	}
	return &ScaffoldResult{
		Config:        cfg,
		PortSource:    portSource,
		AppNameSource: nameSource,
		Stack:         detectStack(abs),
	}, nil
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

// detectDriver prefers compose when a compose file is present, and returns the
// file it found so `driver.compose_file` names the same file the driver runs
// (M14: previously a project with compose.yml was detected as compose but still
// got compose_file = "docker-compose.yml").
func detectDriver(dir string) (driver, composeFile string) {
	if file, ok := findComposeFile(dir); ok {
		return "compose", file
	}
	return "single", ""
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
