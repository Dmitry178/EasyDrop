package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easydrop/internal/core"
	"easydrop/internal/models"
)

func testConfig() *models.Config {
	return &models.Config{
		App:    models.AppConfig{Name: "my-api", Port: 8080},
		Server: models.ServerConfig{Host: "localhost", User: "root"},
		Build:  models.BuildConfig{Strategy: "remote"},
		Driver: models.DriverConfig{Type: "single"},
	}
}

func TestApplyFlags(t *testing.T) {
	cfg := testConfig()
	ApplyFlags(cfg, false, false)
	if cfg.Build.NoCache || cfg.Driver.BlueGreen {
		t.Errorf("flags off must not touch config: %+v", cfg)
	}
	ApplyFlags(cfg, true, true)
	if !cfg.Build.NoCache {
		t.Errorf("no-cache must force Build.NoCache")
	}
	if !cfg.Driver.BlueGreen {
		t.Errorf("blue-green must force Driver.BlueGreen")
	}
	// Flags never unset config-true values.
	cfg2 := testConfig()
	cfg2.Build.NoCache = true
	cfg2.Driver.BlueGreen = true
	ApplyFlags(cfg2, false, false)
	if !cfg2.Build.NoCache || !cfg2.Driver.BlueGreen {
		t.Errorf("flags must not unset config values: %+v", cfg2)
	}
}

func TestResolveAppName(t *testing.T) {
	cfg := testConfig()
	if got, err := ResolveAppName("", cfg); err != nil || got != "my-api" {
		t.Errorf("empty arg must fall back to config: %q, %v", got, err)
	}
	if got, err := ResolveAppName("other-app", cfg); err != nil || got != "other-app" {
		t.Errorf("explicit arg must win: %q, %v", got, err)
	}
	if _, err := ResolveAppName("BAD NAME", cfg); err == nil {
		t.Errorf("invalid arg must fail validation")
	}
	if _, err := ResolveAppName("", nil); err == nil {
		t.Errorf("empty arg without config must fail")
	}
}

func TestNewDriverSwitch(t *testing.T) {
	ex := core.NewLocalExecutor()
	defer ex.Close()
	for typ, want := range map[string]string{
		"single":  "*drivers.SingleDriver",
		"compose": "*drivers.ComposeDriver",
		"swarm":   "*drivers.SwarmDriver",
	} {
		cfg := testConfig()
		cfg.Driver.Type = typ
		drv, err := NewDriver(cfg, ex, "/tmp/ws")
		if err != nil {
			t.Fatalf("NewDriver(%s): %v", typ, err)
		}
		if got := fmt.Sprintf("%T", drv); got != want {
			t.Errorf("NewDriver(%s) = %s, want %s", typ, got, want)
		}
	}
	cfg := testConfig()
	cfg.Driver.Type = "nomad"
	if _, err := NewDriver(cfg, ex, ""); err == nil {
		t.Errorf("NewDriver(nomad) must fail")
	}
}

func TestBuildLocalAndPullRejectsNonSingleDriver(t *testing.T) {
	cfg := testConfig()
	cfg.Driver.Type = "compose"
	cfg.Build.Strategy = "local"
	cfg.Build.Registry = "ghcr.io"
	err := buildLocalAndPull(context.Background(), cfg, &models.Application{Config: cfg}, core.NewLocalExecutor(), t.TempDir(), nil)
	if err == nil {
		t.Fatalf("local strategy with compose must fail")
	}
	if !strings.Contains(err.Error(), "single driver") {
		t.Errorf("error must explain the driver restriction, got: %v", err)
	}
}

func TestBuildLocalAndPullRequiresRegistry(t *testing.T) {
	cfg := testConfig()
	cfg.Build.Strategy = "local" // no registry
	// Validation must happen before any docker call.
	err := buildLocalAndPull(context.Background(), cfg, &models.Application{Config: cfg}, core.NewLocalExecutor(), t.TempDir(), nil)
	if err == nil {
		t.Fatalf("local strategy without registry must fail")
	}
}

func TestTeardownRoutesToDriver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "easydrop.toml")
	body := `
[app]
name = "my-api"
port = 8080
[server]
host = "localhost"
user = "root"
[driver]
type = "single"
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	// Single driver teardown is idempotent: nothing deployed -> nil error,
	// and it must not require the config's app to exist on the host.
	if err := Teardown(context.Background(), path, ""); err != nil {
		t.Errorf("Teardown() on a clean host must be nil, got: %v", err)
	}
}
