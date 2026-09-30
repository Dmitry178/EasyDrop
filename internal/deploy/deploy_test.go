package deploy

import (
	"fmt"
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
