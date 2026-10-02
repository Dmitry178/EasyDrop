package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"easydrop/internal/models"
)

// dotEnvFiles are the optional secret files read from the directory that holds
// easydrop.toml, in increasing priority: `.easydrop.env` overrides `.env`, and
// the real process environment overrides both. Neither file is required – a
// config that references no ${VAR} never needs them.
var dotEnvFiles = []string{".env", ".easydrop.env"}

// envNamePattern is the set of names both the .env parser and ${VAR}
// expansion accept. Anything else is skipped (a .env we did not write) or
// rejected (a typo in easydrop.toml).
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// lookupFunc resolves a variable name to its value.
//
// Sources are composed explicitly instead of mutating the process environment
// with os.Setenv: a secret read from .env must never reach the inherited
// environment of the child `docker build` / `docker push` processes, or it
// would be visible in their /proc/<pid>/environ and in `docker inspect`.
type lookupFunc func(name string) (string, bool)

// envLookup layers the dotenv overlays under the real process environment, so
// an explicit `export` always wins over a file on disk.
func envLookup(overlays ...map[string]string) lookupFunc {
	return func(name string) (string, bool) {
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
		for i := len(overlays) - 1; i >= 0; i-- {
			if v, ok := overlays[i][name]; ok {
				return v, true
			}
		}
		return "", false
	}
}

// resolveSecrets expands ${VAR} references in cfg's string fields using the
// secret files next to the config. dir is the directory holding the config.
func resolveSecrets(cfg *models.Config, dir string) error {
	overlays := make([]map[string]string, 0, len(dotEnvFiles))
	for _, name := range dotEnvFiles {
		vals, err := parseDotEnv(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		overlays = append(overlays, vals)
	}
	return expandConfig(cfg, envLookup(overlays...))
}

// parseDotEnv reads one KEY=VALUE file into a map. A missing file yields nil
// and is not an error.
//
// It implements the small subset EasyDrop needs and stays lenient about the
// rest: `.env` is a filename docker compose also uses, and a file we cannot
// fully model must not break a deploy. Lines that are not assignments, and
// keys outside envNamePattern, are skipped. A misspelled key still fails
// loudly at the point of use, because ${MISSING} is a hard error.
func parseDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if !envNamePattern.MatchString(key) {
			continue
		}
		values[key] = unquoteEnvValue(strings.TrimSpace(value))
	}
	return values, nil
}

// unquoteEnvValue strips one layer of matching quotes. Escape sequences are
// deliberately NOT interpreted, so `${OTHER}` inside a value stays literal and
// a compose .env cannot smuggle values into easydrop's own variables.
func unquoteEnvValue(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// expandableFields is the whitelist of string fields ${VAR} applies to. It is
// an explicit list rather than a reflection walk so that a new config field
// is never interpolated by accident, and so the error messages can name the
// offending field. Numbers and booleans are never interpolated.
var expandableFields = []string{
	"app.name",
	"app.health_check_path",
	"server.host",
	"server.user",
	"server.ssh_key",
	"server.password",
	"build.registry",
	"build.image",
	"driver.compose_file",
	"nginx.domain",
	"nginx.email",
}

// expandConfig resolves every ${VAR} reference in cfg.
func expandConfig(cfg *models.Config, get lookupFunc) error {
	if cfg == nil {
		return fmt.Errorf("server config is nil")
	}
	fields := []struct {
		name string
		ptr  *string
	}{
		{"app.name", &cfg.App.Name},
		{"app.health_check_path", &cfg.App.HealthCheckPath},
		{"server.host", &cfg.Server.Host},
		{"server.user", &cfg.Server.User},
		{"server.ssh_key", &cfg.Server.SSHKey},
		{"server.password", &cfg.Server.Password},
		{"build.registry", &cfg.Build.Registry},
		{"build.image", &cfg.Build.Image},
		{"driver.compose_file", &cfg.Driver.ComposeFile},
		{"nginx.domain", &cfg.Nginx.Domain},
		{"nginx.email", &cfg.Nginx.Email},
	}
	for _, f := range fields {
		expanded, err := expandVars(f.name, *f.ptr, get)
		if err != nil {
			return err
		}
		*f.ptr = expanded
	}
	return nil
}

// expandVars resolves ${VAR} and ${VAR:-default} in a single config value:
//
//	${VAR}       required – an unset or empty variable is a hard error
//	${VAR:-def}  the default applies when VAR is unset or empty
//	$$           a literal "$", so $${VAR} renders as ${VAR}
//
// Defaults are literal (no nested expansion). field is passed in only to
// build the error message: errors name the field and the variable, never the
// field's value, which may be the secret itself.
func expandVars(field, in string, get lookupFunc) (string, error) {
	if !strings.ContainsRune(in, '$') {
		return in, nil
	}
	var out strings.Builder
	for i := 0; i < len(in); {
		if in[i] != '$' {
			out.WriteByte(in[i])
			i++
			continue
		}
		if i+1 < len(in) && in[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}
		if i+1 >= len(in) || in[i+1] != '{' {
			out.WriteByte('$')
			i++
			continue
		}
		end := strings.IndexByte(in[i+2:], '}')
		if end < 0 {
			return "", fmt.Errorf("%s: unterminated ${ – write $$ for a literal dollar sign", field)
		}
		body := in[i+2 : i+2+end]
		i += 2 + end + 1
		resolved, err := resolveRef(field, body, get)
		if err != nil {
			return "", err
		}
		out.WriteString(resolved)
	}
	return out.String(), nil
}

// resolveRef resolves one ${...} body into its literal replacement.
func resolveRef(field, body string, get lookupFunc) (string, error) {
	name, def, hasDefault := strings.Cut(body, ":-")
	if !envNamePattern.MatchString(name) {
		return "", fmt.Errorf("%s: ${%s} is not a valid variable name (letters, digits and _; must not start with a digit)", field, body)
	}
	value, found := get(name)
	if found && value != "" {
		return value, nil
	}
	if hasDefault {
		return def, nil
	}
	// An unset or empty required variable is an error rather than an empty
	// string: deploying with a silently empty password fails much later, on the
	// host, as a confusing authentication error.
	return "", fmt.Errorf("%s: $%s is not set (define it in the environment or in %s)",
		field, name, strings.Join(dotEnvFiles, " / "))
}
