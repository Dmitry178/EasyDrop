package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateSchemaCompleteness(t *testing.T) {
	blob, err := GenerateSchema()
	if err != nil {
		t.Fatalf("GenerateSchema(): %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(blob), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties: %s", blob[:200])
	}
	for _, section := range []string{"app", "server", "build", "driver", "nginx"} {
		if _, ok := props[section]; !ok {
			t.Errorf("schema missing section %q", section)
		}
	}
	text := blob
	for _, want := range []string{
		`"single"`, `"compose"`, `"swarm"`, `"remote"`, `"local"`,
		`"blue_green"`, `"health_check_path"`, `"ssh_key"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("schema must mention %s", want)
		}
	}
	// required detection follows omitempty: app.name and app.port required,
	// health_check_path optional.
	app := props["app"].(map[string]any)
	req, _ := json.Marshal(app["required"])
	if !strings.Contains(string(req), `"name"`) || !strings.Contains(string(req), `"port"`) {
		t.Errorf("app.name and app.port must be required, got %s", req)
	}
	if strings.Contains(string(req), `"health_check_path"`) {
		t.Errorf("app.health_check_path must be optional (omitempty), got %s", req)
	}
}
