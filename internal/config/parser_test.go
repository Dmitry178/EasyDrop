package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempTOML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "easydrop.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestParseValidFullConfig(t *testing.T) {
	path := writeTempTOML(t, `
[app]
name = "my-awesome-api"
port = 8080
health_check_path = "/health"

[server]
host = "185.178.21.42"
user = "root"
ssh_key = "~/.ssh/id_rsa"
port = 2222

[build]
strategy = "remote"

[driver]
type = "solo"

[nginx]
domain = "my-project.com"
ssl = true
email = "admin@my-project.com"
`)
	cfg, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig() unexpected error: %v", err)
	}
	if cfg.App.Name != "my-awesome-api" {
		t.Errorf("App.Name = %q, want %q", cfg.App.Name, "my-awesome-api")
	}
	if cfg.App.Port != 8080 {
		t.Errorf("App.Port = %d, want 8080", cfg.App.Port)
	}
	if cfg.Server.Host != "185.178.21.42" {
		t.Errorf("Server.Host = %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 2222 {
		t.Errorf("Server.Port = %d, want 2222", cfg.Server.Port)
	}
	if cfg.Nginx.Domain != "my-project.com" {
		t.Errorf("Nginx.Domain = %q", cfg.Nginx.Domain)
	}
	if !cfg.Nginx.SSL {
		t.Errorf("Nginx.SSL = false, want true")
	}
}

func TestParseMissingRequiredFields(t *testing.T) {
	// Missing app.name
	path := writeTempTOML(t, `
[app]
port = 8080
[server]
host = "example.com"
user = "root"
[nginx]
domain = "example.com"
`)
	if _, err := ParseConfig(path); err == nil {
		t.Errorf("ParseConfig() expected error for missing app.name, got nil")
	}

	// Missing server.host
	path2 := writeTempTOML(t, `
[app]
name = "api"
port = 8080
[server]
user = "root"
[nginx]
domain = "example.com"
`)
	if _, err := ParseConfig(path2); err == nil {
		t.Errorf("ParseConfig() expected error for missing server.host, got nil")
	}
}

func TestParseDefaults(t *testing.T) {
	path := writeTempTOML(t, `
[app]
name = "api"
port = 3000
[server]
host = "localhost"
user = "root"
[nginx]
domain = "example.com"
`)
	cfg, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig() unexpected error: %v", err)
	}
	if cfg.Server.Port != 22 {
		t.Errorf("Server.Port = %d, want default 22", cfg.Server.Port)
	}
	if cfg.Build.Strategy != "remote" {
		t.Errorf("Build.Strategy = %q, want default %q", cfg.Build.Strategy, "remote")
	}
	if cfg.Driver.Type != "solo" {
		t.Errorf("Driver.Type = %q, want default %q", cfg.Driver.Type, "solo")
	}
	if cfg.Driver.BlueGreen {
		t.Errorf("Driver.BlueGreen = true, want default false (Blue-Green is opt-in)")
	}
	if cfg.App.HealthCheckPath != "/" {
		t.Errorf("App.HealthCheckPath = %q, want default %q", cfg.App.HealthCheckPath, "/")
	}
	home, _ := os.UserHomeDir()
	wantKey := filepath.Join(home, ".ssh/id_rsa")
	if cfg.Server.SSHKey != wantKey {
		t.Errorf("Server.SSHKey = %q, want expanded %q", cfg.Server.SSHKey, wantKey)
	}
	// Compose default file
	path2 := writeTempTOML(t, `
[app]
name = "api"
port = 3000
[server]
host = "localhost"
user = "root"
[driver]
type = "compose"
[nginx]
domain = "example.com"
`)
	cfg2, err := ParseConfig(path2)
	if err != nil {
		t.Fatalf("ParseConfig() unexpected error: %v", err)
	}
	if cfg2.Driver.ComposeFile != "docker-compose.yml" {
		t.Errorf("Driver.ComposeFile = %q, want default docker-compose.yml", cfg2.Driver.ComposeFile)
	}
}
