package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"easydrop/internal/models"
)

// fieldMeta carries human documentation that reflection cannot infer.
type fieldMeta struct {
	desc    string
	def     any
	enum    []string
	minimum *int
	maximum *int
}

func intPtr(v int) *int { return &v }

// schemaMeta documents every leaf of models.Config. Adding a config field
// without a meta entry fails TestGenerateSchemaCompleteness — update both.
var schemaMeta = map[string]fieldMeta{
	"app.name":              {desc: "docker-compatible lowercase name"},
	"app.port":              {desc: "port the app listens on inside the container", minimum: intPtr(1), maximum: intPtr(65535)},
	"app.host_port":         {desc: "port published on the host (defaults to app.port); the Blue-Green pair is {host_port, host_port+1}", minimum: intPtr(1), maximum: intPtr(65534)},
	"app.health_check_path": {desc: "HTTP path probed for 200 OK during deploys", def: "/"},
	"server.host":           {desc: "IP/hostname, or localhost/127.0.0.1 for local deploys"},
	"server.user":           {desc: "SSH user (unused for localhost)"},
	"server.ssh_key":        {desc: "private key path, ~ expands", def: "~/.ssh/id_rsa"},
	"server.password":       {desc: "SSH password fallback (never logged)"},
	"server.port":           {desc: "SSH port", def: 22},
	"build.strategy":        {desc: "remote builds on the host (no registry); local builds and pushes", enum: []string{"remote", "local"}, def: "remote"},
	"build.registry":        {desc: "registry for local builds (FR-04)"},
	"build.image":           {desc: "image override for local builds"},
	"build.no_cache":        {desc: "bypass Docker build cache (deploy --no-cache forces true)", def: false},
	"driver.type":           {desc: "orchestration driver", enum: []string{"single", "compose", "swarm"}, def: "single"},
	"driver.compose_file":   {desc: "compose file for compose/swarm drivers", def: "docker-compose.yml"},
	"driver.blue_green":     {desc: "zero-downtime Blue-Green swaps, single driver only (deploy --blue-green forces true)", def: false},
	"nginx.domain":          {desc: "public domain for reverse proxy + TLS (empty skips ingress)"},
	"nginx.ssl":             {desc: "provision Let's Encrypt certificates"},
	"nginx.email":           {desc: "ACME contact; empty registers without email"},
}

// GenerateSchema renders the easydrop.toml JSON Schema (draft-07) directly
// from the models structs, so the MCP resource can never drift from the
// parser. Leaf documentation comes from schemaMeta.
func GenerateSchema() (string, error) {
	root := map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title":   "easydrop.toml",
		"type":    "object",
	}
	props := map[string]any{}
	t := reflect.TypeOf(models.Config{})
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		name, _, _ := strings.Cut(sf.Tag.Get("toml"), ",")
		if name == "" || name == "-" {
			continue
		}
		obj, err := structSchema(sf.Type, name)
		if err != nil {
			return "", err
		}
		props[name] = obj
	}
	root["properties"] = props
	blob, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal schema: %w", err)
	}
	return string(blob), nil
}

func structSchema(t reflect.Type, prefix string) (map[string]any, error) {
	props := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("toml")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		prop, err := leafSchema(sf.Type, prefix+"."+name)
		if err != nil {
			return nil, err
		}
		props[name] = prop
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
		}
	}
	obj := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		obj["required"] = required
	}
	return obj, nil
}

func leafSchema(t reflect.Type, path string) (map[string]any, error) {
	if t.Kind() == reflect.Struct {
		return structSchema(t, path)
	}
	meta, ok := schemaMeta[path]
	if !ok {
		return nil, fmt.Errorf("no schemaMeta for config field %q: add documentation", path)
	}
	var typ string
	switch t.Kind() {
	case reflect.String:
		typ = "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		typ = "integer"
	case reflect.Bool:
		typ = "boolean"
	default:
		return nil, fmt.Errorf("unsupported config field type %s at %q", t.Kind(), path)
	}
	prop := map[string]any{"type": typ}
	if meta.desc != "" {
		prop["description"] = meta.desc
	}
	if meta.def != nil {
		prop["default"] = meta.def
	}
	if len(meta.enum) > 0 {
		prop["enum"] = meta.enum
	}
	if meta.minimum != nil {
		prop["minimum"] = *meta.minimum
	}
	if meta.maximum != nil {
		prop["maximum"] = *meta.maximum
	}
	return prop, nil
}
