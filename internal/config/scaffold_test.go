package config

import (
	"os"
	"path/filepath"
	"testing"
)

func mkScaffoldDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScaffoldDockerfileExpose(t *testing.T) {
	dir := mkScaffoldDir(t, map[string]string{
		"Dockerfile": "FROM nginx:alpine\nEXPOSE 3000\n",
		"main.go":    "package main\n",
	})
	cfg, err := Scaffold(dir)
	if err != nil {
		t.Fatalf("Scaffold() unexpected error: %v", err)
	}
	if cfg.App.Port != 3000 {
		t.Errorf("App.Port = %d, want 3000 from EXPOSE", cfg.App.Port)
	}
	if cfg.Driver.Type != "single" {
		t.Errorf("Driver.Type = %q, want single", cfg.Driver.Type)
	}
	if cfg.Build.Strategy != "remote" {
		t.Errorf("Build.Strategy = %q, want remote", cfg.Build.Strategy)
	}
	if cfg.Server.Host != "localhost" {
		t.Errorf("Server.Host = %q, want localhost", cfg.Server.Host)
	}
}

func TestScaffoldComposeDetection(t *testing.T) {
	dir := mkScaffoldDir(t, map[string]string{
		"docker-compose.yml": "services:\n  web:\n    image: x\n",
	})
	cfg, err := Scaffold(dir)
	if err != nil {
		t.Fatalf("Scaffold() unexpected error: %v", err)
	}
	if cfg.Driver.Type != "compose" {
		t.Errorf("Driver.Type = %q, want compose", cfg.Driver.Type)
	}
	if cfg.Driver.ComposeFile != "docker-compose.yml" {
		t.Errorf("ComposeFile = %q", cfg.Driver.ComposeFile)
	}
}

func TestScaffoldBareDirDefaults(t *testing.T) {
	dir := mkScaffoldDir(t, map[string]string{
		"package.json": "{}\n",
	})
	cfg, err := Scaffold(dir)
	if err != nil {
		t.Fatalf("Scaffold() unexpected error: %v", err)
	}
	// OD-04: nothing stated a port, so none is invented – not even 8080.
	if cfg.App.Port != 0 {
		t.Errorf("App.Port = %d, want 0 (no port detected, no default)", cfg.App.Port)
	}
	if !cfg.PortMissing() {
		t.Errorf("PortMissing() = false, want true")
	}
	// Every other default still applies.
	if cfg.Driver.Type != "single" {
		t.Errorf("Driver.Type = %q, want single", cfg.Driver.Type)
	}
	if cfg.Build.Strategy != "remote" {
		t.Errorf("Build.Strategy = %q, want remote", cfg.Build.Strategy)
	}
	if cfg.Server.Host != "localhost" || cfg.Server.Port != 22 {
		t.Errorf("Server defaults not applied: %+v", cfg.Server)
	}
	if cfg.App.HealthCheckPath != "/" {
		t.Errorf("HealthCheckPath = %q, want /", cfg.App.HealthCheckPath)
	}
	// host_port must not be fabricated from an absent port.
	if cfg.App.HostPort != 0 {
		t.Errorf("App.HostPort = %d, want 0", cfg.App.HostPort)
	}
	if cfg.App.Name == "" {
		t.Errorf("App.Name must default to directory name")
	}
}

func TestSanitizeAppName(t *testing.T) {
	cases := map[string]string{
		"my-app":         "my-app",
		"My_Awesome.API": "my_awesome.api",
		"with space!":    "with-space",
		"UPPER":          "upper",
	}
	for in, want := range cases {
		if got := sanitizeAppName(in); got != want {
			t.Errorf("sanitizeAppName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteConfigRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "easydrop.toml")
	cfg, err := Scaffold(mkScaffoldDir(t, map[string]string{"Dockerfile": "FROM x\nEXPOSE 8080\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(path, cfg.Config, false); err != nil {
		t.Fatalf("first WriteConfig: %v", err)
	}
	if err := WriteConfig(path, cfg.Config, false); err == nil {
		t.Errorf("second WriteConfig without force must fail")
	}
	if err := WriteConfig(path, cfg.Config, true); err != nil {
		t.Errorf("WriteConfig with force must succeed: %v", err)
	}
	// Round-trip: written file must parse.
	parsed, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("written config must parse: %v", err)
	}
	if parsed.App.Name != cfg.App.Name || parsed.App.Port != cfg.App.Port {
		t.Errorf("round-trip mismatch: %+v vs %+v", parsed.App, cfg.App)
	}
}
