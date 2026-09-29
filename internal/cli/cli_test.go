package cli

import (
	"testing"

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

func TestApplyDeployFlags(t *testing.T) {
	cfg := testConfig()
	applyDeployFlags(cfg, false, false)
	if cfg.Build.NoCache || cfg.Driver.BlueGreen {
		t.Errorf("flags off must not touch config: %+v", cfg)
	}
	applyDeployFlags(cfg, true, true)
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
	applyDeployFlags(cfg2, false, false)
	if !cfg2.Build.NoCache || !cfg2.Driver.BlueGreen {
		t.Errorf("flags must not unset config values: %+v", cfg2)
	}
}

func TestResolveAppName(t *testing.T) {
	cfg := testConfig()
	if got, err := resolveAppName("", cfg); err != nil || got != "my-api" {
		t.Errorf("empty arg must fall back to config: %q, %v", got, err)
	}
	if got, err := resolveAppName("other-app", cfg); err != nil || got != "other-app" {
		t.Errorf("explicit arg must win: %q, %v", got, err)
	}
	if _, err := resolveAppName("BAD NAME", cfg); err == nil {
		t.Errorf("invalid arg must fail validation")
	}
	if _, err := resolveAppName("", nil); err == nil {
		t.Errorf("empty arg without config must fail")
	}
}

func TestRootHasAllCommands(t *testing.T) {
	root := rootCmd()
	want := map[string]bool{"init": false, "deploy": false, "status": false, "logs": false, "rollback": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("root command missing %q", name)
		}
	}
}
