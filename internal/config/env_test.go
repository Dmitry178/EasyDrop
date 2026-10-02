package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easydrop/internal/models"
)

// lookupStub resolves a fixed map, so the tests never depend on the ambient
// process environment.
func lookupStub(vals map[string]string) lookupFunc {
	return func(name string) (string, bool) {
		v, ok := vals[name]
		return v, ok
	}
}

// seededConfig puts a ${VAR} reference in every expandable field, named after
// the field, so a single expansion pass covers the whole whitelist.
func seededConfig() *models.Config {
	return &models.Config{
		App:    models.AppConfig{Name: "${APP}", HealthCheckPath: "${HEALTH}"},
		Server: models.ServerConfig{Host: "${HOST}", User: "${USER}", SSHKey: "${KEY}", Password: "${PASS}"},
		Build:  models.BuildConfig{Registry: "${REG}", Image: "${IMG}"},
		Driver: models.DriverConfig{ComposeFile: "${COMPOSE}"},
		Nginx:  models.NginxConfig{Domain: "${DOMAIN}", Email: "${EMAIL}"},
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestExpandVars(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		env     map[string]string
		want    string
		wantErr string
	}{
		{name: "no dollar stays untouched", in: "/plain/value", want: "/plain/value"},
		{name: "required var", in: "pw-${SECRET}", env: map[string]string{"SECRET": "s3cret"}, want: "pw-s3cret"},
		{name: "var at both ends", in: "${A}:${B}", env: map[string]string{"A": "1", "B": "2"}, want: "1:2"},
		{name: "unset var fails", in: "${SECRET}", wantErr: "$SECRET is not set"},
		{name: "empty var fails", in: "x${SECRET}", env: map[string]string{"SECRET": ""}, wantErr: "$SECRET is not set"},
		{name: "default when unset", in: "${SECRET:-fallback}", want: "fallback"},
		{name: "default when empty", in: "${SECRET:-fallback}", env: map[string]string{"SECRET": ""}, want: "fallback"},
		{name: "value beats default", in: "${SECRET:-fallback}", env: map[string]string{"SECRET": "real"}, want: "real"},
		{name: "empty default", in: "${SECRET:-}", want: ""},
		{name: "escaped dollars", in: "$${SECRET}", env: map[string]string{"SECRET": "s3cret"}, want: "${SECRET}"},
		{name: "double escape", in: "$$${SECRET}", env: map[string]string{"SECRET": "s3cret"}, want: "$s3cret"},
		{name: "lone dollar kept", in: "100$ and ${SECRET}", env: map[string]string{"SECRET": "x"}, want: "100$ and x"},
		{name: "default is literal, not expanded", in: "${A:-${B}}", want: "${B}"},
		{name: "nested reference in value is not re-scanned", in: "${A:-x}", env: map[string]string{"A": "${B}"}, want: "${B}"},
		{name: "unterminated reference", in: "${SECRET", wantErr: "unterminated ${"},
		{name: "bad name", in: "${1BAD}", wantErr: "not a valid variable name"},
		{name: "empty name", in: "${}", wantErr: "not a valid variable name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandVars("server.password", tc.in, lookupStub(tc.env))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expandVars(%q) = %q, want error containing %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("expandVars(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("expandVars(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestExpandVarsErrorOmitsValue is the NFR-02 guard for this feature: a
// missing-variable error must name the field and the variable, never echo the
// surrounding value, which is exactly where the secret sits.
func TestExpandVarsErrorOmitsValue(t *testing.T) {
	const secret = "hunter2-do-not-log"
	_, err := expandVars("server.password", "prefix-"+secret+"-${MISSING_VAR}", lookupStub(nil))
	if err == nil {
		t.Fatal("expandVars must fail on an unset required variable")
	}
	msg := err.Error()
	if strings.Contains(msg, secret) {
		t.Errorf("error leaks the field value: %q", msg)
	}
	if !strings.Contains(msg, "server.password") || !strings.Contains(msg, "MISSING_VAR") {
		t.Errorf("error must name the field and the variable, got %q", msg)
	}
	if !strings.Contains(msg, ".easydrop.env") {
		t.Errorf("error should point at the secret files, got %q", msg)
	}
}

func TestExpandConfigCoversWhitelist(t *testing.T) {
	env := map[string]string{
		"APP":     "from-env-app",
		"HEALTH":  "/ready",
		"HOST":    "198.51.100.7",
		"USER":    "deploy",
		"KEY":     "~/.ssh/id_ecdsa",
		"PASS":    "s3cret",
		"REG":     "ghcr.io/team",
		"IMG":     "web:9.9",
		"COMPOSE": "docker-compose.prod.yml",
		"DOMAIN":  "app.example.com",
		"EMAIL":   "ops@example.com",
	}
	cfg := seededConfig()
	if err := expandConfig(cfg, lookupStub(env)); err != nil {
		t.Fatalf("expandConfig: %v", err)
	}
	checks := []struct{ got, want string }{
		{cfg.App.Name, "from-env-app"},
		{cfg.App.HealthCheckPath, "/ready"},
		{cfg.Server.Host, "198.51.100.7"},
		{cfg.Server.User, "deploy"},
		{cfg.Server.SSHKey, "~/.ssh/id_ecdsa"},
		{cfg.Server.Password, "s3cret"},
		{cfg.Build.Registry, "ghcr.io/team"},
		{cfg.Build.Image, "web:9.9"},
		{cfg.Driver.ComposeFile, "docker-compose.prod.yml"},
		{cfg.Nginx.Domain, "app.example.com"},
		{cfg.Nginx.Email, "ops@example.com"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// TestExpandConfigMatchesWhitelist keeps the two lists in sync: a field added
// to models must be listed in expandableFields, and every listed field must
// actually be reachable. Renaming a field without touching env.go would
// otherwise silently stop interpolating it.
func TestExpandConfigMatchesWhitelist(t *testing.T) {
	if len(expandableFields) != 11 {
		t.Errorf("expandableFields has %d entries, want 11 – update the list and TestExpandConfigCoversWhitelist together", len(expandableFields))
	}
	seen := map[string]bool{}
	for _, name := range expandableFields {
		if seen[name] {
			t.Errorf("expandableFields lists %q twice", name)
		}
		seen[name] = true
	}
	for _, name := range []string{"server.password", "server.ssh_key", "app.name", "nginx.domain"} {
		if !seen[name] {
			t.Errorf("%q missing from expandableFields", name)
		}
	}
}

func TestParseDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", `
# a comment
APP_NAME="quoted value"
SINGLE='raw $NOT_EXPANDED'
export EXPORTED=yes

  SPACED   =   trimmed
EMPTY=
INLINE=has spaces
not an assignment
9INVALID=nope
DUPLICATE=first
DUPLICATE=second
`)
	vals, err := parseDotEnv(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("parseDotEnv: %v", err)
	}
	want := map[string]string{
		"APP_NAME":  "quoted value",
		"SINGLE":    "raw $NOT_EXPANDED",
		"EXPORTED":  "yes",
		"SPACED":    "trimmed",
		"EMPTY":     "",
		"INLINE":    "has spaces",
		"DUPLICATE": "second",
	}
	if len(vals) != len(want) {
		t.Fatalf("parsed %d keys (%v), want %d", len(vals), vals, len(want))
	}
	for k, v := range want {
		if vals[k] != v {
			t.Errorf("%s = %q, want %q", k, vals[k], v)
		}
	}
}

func TestParseDotEnvMissingFileIsNotAnError(t *testing.T) {
	vals, err := parseDotEnv(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatalf("parseDotEnv on a missing file: %v", err)
	}
	if vals != nil {
		t.Errorf("parseDotEnv on a missing file = %v, want nil", vals)
	}
}

// TestParseConfigResolvesSecrets covers the full path: ParseConfig resolves
// references before validating, and before ApplyDefaults expands "~".
func TestParseConfigResolvesSecrets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".easydrop.env", "EASYDROP_SSH_PASSWORD=from-easydrop-env\n")
	writeFile(t, dir, ".env", "EASYDROP_SSH_PASSWORD=from-plain-env\nEASYDROP_REGISTRY=ghcr.io/team\n")
	path := filepath.Join(dir, "easydrop.toml")
	writeFile(t, dir, "easydrop.toml", `
[app]
name = "my-awesome-api"
port = 8080

[server]
host = "203.0.113.10"
user = "deploy"
ssh_key = "${EASYDROP_SSH_KEY:-~/.ssh/id_ed25519}"
password = "${EASYDROP_SSH_PASSWORD}"

[build]
strategy = "local"
registry = "${EASYDROP_REGISTRY}"
`)
	cfg, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Server.Password != "from-easydrop-env" {
		t.Errorf("password = %q, want the .easydrop.env value to win", cfg.Server.Password)
	}
	if cfg.Build.Registry != "ghcr.io/team" {
		t.Errorf("registry = %q, want it resolved from .env", cfg.Build.Registry)
	}
	// The ":-" default was used, and "~" expansion still ran afterwards.
	if !strings.HasSuffix(cfg.Server.SSHKey, "/.ssh/id_ed25519") || strings.HasPrefix(cfg.Server.SSHKey, "~") {
		t.Errorf("ssh_key = %q, want the default with ~ expanded", cfg.Server.SSHKey)
	}
}

func TestParseConfigProcessEnvWinsOverFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "EASYDROP_TEST_PW=from-file\n")
	path := filepath.Join(dir, "easydrop.toml")
	writeFile(t, dir, "easydrop.toml", `
[app]
name = "my-awesome-api"
port = 8080

[server]
host = "203.0.113.10"
password = "${EASYDROP_TEST_PW}"
`)
	t.Setenv("EASYDROP_TEST_PW", "from-process-env")
	cfg, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Server.Password != "from-process-env" {
		t.Errorf("password = %q, want the process environment to win", cfg.Server.Password)
	}
}

func TestParseConfigMissingSecretFailsBeforeConnecting(t *testing.T) {
	path := writeTempTOML(t, `
[app]
name = "my-awesome-api"
port = 8080

[server]
host = "203.0.113.10"
password = "${EASYDROP_DEFINITELY_UNSET_PASSWORD}"
`)
	_, err := ParseConfig(path)
	if err == nil {
		t.Fatal("ParseConfig must fail when a required variable is unset")
	}
	if !strings.Contains(err.Error(), "EASYDROP_DEFINITELY_UNSET_PASSWORD") {
		t.Errorf("error must name the missing variable, got %q", err)
	}
}

// TestParseConfigWithoutDotEnvUnchanged keeps the common case honest: a config
// with no references and no secret files parses exactly as before.
func TestParseConfigWithoutDotEnvUnchanged(t *testing.T) {
	path := writeTempTOML(t, `
[app]
name = "my-awesome-api"
port = 8080

[server]
host = "203.0.113.10"
user = "deploy"
ssh_key = "~/.ssh/id_rsa"

[build]
strategy = "remote"
`)
	cfg, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.App.Name != "my-awesome-api" || cfg.Server.Host != "203.0.113.10" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if cfg.Build.NoCache || cfg.Driver.BlueGreen || cfg.Nginx.SSL {
		t.Errorf("defaults changed: %+v", cfg)
	}
}
