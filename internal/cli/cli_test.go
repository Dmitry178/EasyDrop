package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootHasAllCommands(t *testing.T) {
	root := rootCmd()
	want := map[string]bool{"init": false, "deploy": false, "status": false, "logs": false, "rollback": false, "teardown": false, "mcp-server": false}
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

// TestInitCommandEndToEnd runs `init` for real in a temp project: the flag has
// to reach the config, and the report has to say where the port came from.
func TestInitCommandEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	run := func(args ...string) string {
		out := &bytes.Buffer{}
		root := rootCmd()
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("easydrop %v: %v\n%s", args, err, out)
		}
		return out.String()
	}

	// No port anywhere: nothing may be invented (OD-04).
	out := run("init")
	if !strings.Contains(out, "NOT SET") || !strings.Contains(out, "ACTION REQUIRED") {
		t.Errorf("expected the missing-port block, got:\n%s", out)
	}
	written, err := os.ReadFile(filepath.Join(dir, "easydrop.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "port = 80") {
		t.Errorf("no port must be written, got:\n%s", written)
	}

	// --port must override detection and be reported as such.
	out = run("init", "--force", "--port", "8080")
	if !strings.Contains(out, "port    8080 (explicit override)") {
		t.Errorf("--port not reflected in the report:\n%s", out)
	}
	written, err = os.ReadFile(filepath.Join(dir, "easydrop.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "port = 8080") {
		t.Errorf("--port not written to the config, got:\n%s", written)
	}

	// An invalid port must be rejected rather than silently ignored.
	root := rootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"init", "--force", "--port", "70000"})
	if err := root.Execute(); err == nil {
		t.Errorf("--port 70000 must fail")
	}
}

// TestLogsFlagsWired checks the `logs` command exposes the filter flag the
// MCP `get_logs` tool has, so the two interfaces stay in parity.
func TestLogsFlagsWired(t *testing.T) {
	var logs *cobra.Command
	for _, c := range rootCmd().Commands() {
		if c.Name() == "logs" {
			logs = c
		}
	}
	if logs == nil {
		t.Fatal("logs command missing")
	}
	for name, def := range map[string]string{"tail": "100", "grep": ""} {
		f := logs.Flags().Lookup(name)
		if f == nil {
			t.Errorf("logs missing --%s", name)
			continue
		}
		if f.DefValue != def {
			t.Errorf("--%s default = %q, want %q", name, f.DefValue, def)
		}
	}
	if f := logs.Flags().Lookup("grep"); f != nil && f.Usage == "" {
		t.Errorf("--grep needs a usage string (it takes a regexp)")
	}
}

// TestLogsInvalidGrepFails runs the command for real: an unusable expression
// must be a non-zero exit, never a silently unfiltered dump.
func TestLogsInvalidGrepFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "easydrop.toml"),
		[]byte("[app]\nname = \"my-api\"\nport = 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	root := rootCmd()
	root.SetArgs([]string{"logs", "--grep", "error("})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Fatalf("invalid --grep must fail the command")
	}
}
