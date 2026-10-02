package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"easydrop/internal/models"
)

// examplesDir is the documentation directory holding one .toml per config case.
const examplesDir = "../../examples"

// exampleCases pins the expectations that make each example meaningful, so an
// example cannot silently drift into a copy of another one.
var exampleCases = map[string]struct {
	driver   string
	strategy string
	domain   string
	ssl      bool
	bg       bool
}{
	"01-minimal.toml":                         {driver: "single", strategy: "remote"},
	"02-localhost-dev.toml":                   {driver: "single", strategy: "remote"},
	"03-remote-ssh-key.toml":                  {driver: "single", strategy: "remote"},
	"04-remote-ssh-password.toml":             {driver: "single", strategy: "remote"},
	"05-remote-ssh-custom-port.toml":          {driver: "single", strategy: "remote"},
	"06-remote-nginx-ssl.toml":                {driver: "single", strategy: "remote", domain: "api.example.com", ssl: true, bg: true},
	"07-blue-green.toml":                      {driver: "single", strategy: "remote", domain: "api.example.com", ssl: true, bg: true},
	"08-build-no-cache.toml":                  {driver: "single", strategy: "remote"},
	"09-healthcheck-path.toml":                {driver: "single", strategy: "remote"},
	"10-nginx-plain-http.toml":                {driver: "single", strategy: "remote", domain: "staging.example.com"},
	"11-build-local-registry.toml":            {driver: "single", strategy: "local"},
	"12-build-local-registry-pinned-tag.toml": {driver: "single", strategy: "local", domain: "api.example.com", ssl: true, bg: true},
	"13-compose.toml":                         {driver: "compose", strategy: "remote"},
	"14-compose-custom-file.toml":             {driver: "compose", strategy: "remote"},
	"15-swarm.toml":                           {driver: "swarm", strategy: "remote"},
	"16-compose-nginx-ssl.toml":               {driver: "compose", strategy: "remote", domain: "shop.example.com", ssl: true},
	"20-secrets-from-env.toml":                {driver: "single", strategy: "local", domain: "api.example.com", ssl: true, bg: true},
	"99-full-reference.toml":                  {driver: "single", strategy: "remote", domain: "api.example.com", ssl: true, bg: true},
}

func exampleFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(examplesDir, "*.toml"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no example configs found in %s", examplesDir)
	}
	return paths
}

// TestExampleConfigsAreDocumented keeps the map above and the directory in
// sync: a new example must declare what it is meant to demonstrate.
func TestExampleConfigsAreDocumented(t *testing.T) {
	for _, path := range exampleFiles(t) {
		if _, ok := exampleCases[filepath.Base(path)]; !ok {
			t.Errorf("example %s is not covered by exampleCases in %s", filepath.Base(path), "internal/config/examples_test.go")
		}
	}
	for name := range exampleCases {
		if _, err := os.Stat(filepath.Join(examplesDir, name)); err != nil {
			t.Errorf("example %s is referenced by exampleCases but missing: %v", name, err)
		}
	}
}

// TestExampleConfigsParse runs every example through the real parser and
// defaults, so a typo or an invalid value fails here instead of in a user's
// terminal.
func TestExampleConfigsParse(t *testing.T) {
	for _, path := range exampleFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if filepath.Base(path) == "20-secrets-from-env.toml" {
				// The example deliberately references a required variable, so
				// supply it here to prove the reference resolves.
				t.Setenv("EASYDROP_SSH_PASSWORD", "example-placeholder")
			}
			cfg, err := ParseConfig(path)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if err := models.ValidateAppName(cfg.App.Name); err != nil {
				t.Errorf("app.name: %v", err)
			}
			if cfg.App.Port < 1 || cfg.App.HostPort < 1 || cfg.App.HostPort > 65534 {
				t.Errorf("ports out of range: port=%d host_port=%d", cfg.App.Port, cfg.App.HostPort)
			}
			want := exampleCases[filepath.Base(path)]
			if cfg.Driver.Type != want.driver {
				t.Errorf("driver.type = %q, want %q", cfg.Driver.Type, want.driver)
			}
			if cfg.Build.Strategy != want.strategy {
				t.Errorf("build.strategy = %q, want %q", cfg.Build.Strategy, want.strategy)
			}
			if cfg.Nginx.Domain != want.domain {
				t.Errorf("nginx.domain = %q, want %q", cfg.Nginx.Domain, want.domain)
			}
			if cfg.Nginx.SSL != want.ssl {
				t.Errorf("nginx.ssl = %v, want %v", cfg.Nginx.SSL, want.ssl)
			}
			if cfg.Driver.BlueGreen != want.bg {
				t.Errorf("driver.blue_green = %v, want %v", cfg.Driver.BlueGreen, want.bg)
			}
			if cfg.Build.Strategy == "local" && cfg.Build.Registry == "" {
				t.Error(`build.strategy = "local" without build.registry`)
			}
		})
	}
}

// TestExampleConfigsUseKnownKeys rejects unknown keys. go-toml ignores them, so
// a misspelled option in an example would otherwise be copied by users and
// silently do nothing.
func TestExampleConfigsUseKnownKeys(t *testing.T) {
	known := map[string]map[string]bool{}
	tpe := reflect.TypeOf(models.Config{})
	for i := 0; i < tpe.NumField(); i++ {
		section, _, _ := strings.Cut(tpe.Field(i).Tag.Get("toml"), ",")
		known[section] = map[string]bool{}
		st := tpe.Field(i).Type
		for j := 0; j < st.NumField(); j++ {
			key, _, _ := strings.Cut(st.Field(j).Tag.Get("toml"), ",")
			known[section][key] = true
		}
	}
	for _, path := range exampleFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var raw map[string]map[string]any
		if err := toml.Unmarshal(data, &raw); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		for section, keys := range raw {
			allowed, ok := known[section]
			if !ok {
				t.Errorf("%s: unknown section [%s]", filepath.Base(path), section)
				continue
			}
			for key := range keys {
				if !allowed[key] {
					t.Errorf("%s: unknown key %s.%s", filepath.Base(path), section, key)
				}
			}
		}
	}
}
