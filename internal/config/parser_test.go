package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easydrop/internal/models"
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
type = "single"

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
	if cfg.Driver.Type != "single" {
		t.Errorf("Driver.Type = %q, want default %q", cfg.Driver.Type, "single")
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

func TestPortSplitDefaults(t *testing.T) {
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
	if cfg.App.HostPort != cfg.App.Port {
		t.Errorf("HostPort = %d, must default to Port %d", cfg.App.HostPort, cfg.App.Port)
	}
}

func TestPortSplitExplicitHostPort(t *testing.T) {
	path := writeTempTOML(t, `
[app]
name = "api"
port = 80
host_port = 18080
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
	if cfg.App.Port != 80 || cfg.App.HostPort != 18080 {
		t.Errorf("Port/HostPort = %d/%d, want 80/18080", cfg.App.Port, cfg.App.HostPort)
	}
}

func TestPortValidation(t *testing.T) {
	cases := map[string]string{
		"zero port": `
[app]
name = "api"
[server]
host = "localhost"
user = "root"`,
		"port too high": `
[app]
name = "api"
port = 70000
[server]
host = "localhost"
user = "root"`,
		"host_port too high": `
[app]
name = "api"
port = 80
host_port = 99999
[server]
host = "localhost"
user = "root"`,
		"host_port at ceiling leaves no blue-green port": `
[app]
name = "api"
port = 80
host_port = 65535
[server]
host = "localhost"
user = "root"`,
	}
	for name, tomlBody := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(writeTempTOML(t, tomlBody)); err == nil {
				t.Errorf("ParseConfig() expected error, got nil")
			}
		})
	}
}

func TestValidateNginx(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     models.NginxConfig
		wantErr string
	}{
		{"no tls", models.NginxConfig{}, ""},
		{"letsencrypt", models.NginxConfig{Domain: "api.example.com", SSL: true}, ""},
		{"self-signed localhost", models.NginxConfig{Domain: "localhost", SSL: true, SelfSigned: true}, ""},
		{"self-signed ip", models.NginxConfig{Domain: "127.0.0.1", SSL: true, SelfSigned: true}, ""},
		{"self-signed internal name", models.NginxConfig{Domain: "api.internal.test", SSL: true, SelfSigned: true}, ""},
		// self_signed selects HOW the certificate is obtained, so it cannot stand
		// alone – accepting it would leave the user wondering why no TLS appeared.
		{"self-signed without ssl", models.NginxConfig{Domain: "localhost", SelfSigned: true},
			"nginx.self_signed requires nginx.ssl = true"},
		{"ssl without domain", models.NginxConfig{SSL: true},
			"requires a non-empty nginx.domain"},
		{"self-signed without domain", models.NginxConfig{SSL: true, SelfSigned: true},
			"requires a non-empty nginx.domain"},
		// A wildcard passes validateDomain (it is legal in server_name) but is
		// useless in a leaf certificate: browsers reject a self-signed wildcard
		// outside the issuer's own domain, and it does not match the bare host.
		{"self-signed wildcard", models.NginxConfig{Domain: "*.example.com", SSL: true, SelfSigned: true},
			"wildcard domain"},
		{"self-signed injection", models.NginxConfig{Domain: "a b.com", SSL: true, SelfSigned: true},
			"must be a hostname"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNginx(&tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
