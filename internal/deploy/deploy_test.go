package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestBuildLocalAndPullRejectsNonSingleDriver(t *testing.T) {
	cfg := testConfig()
	cfg.Driver.Type = "compose"
	cfg.Build.Strategy = "local"
	cfg.Build.Registry = "ghcr.io"
	err := buildLocalAndPull(context.Background(), cfg, &models.Application{Config: cfg}, core.NewLocalExecutor(), t.TempDir(), nil)
	if err == nil {
		t.Fatalf("local strategy with compose must fail")
	}
	if !strings.Contains(err.Error(), "single driver") {
		t.Errorf("error must explain the driver restriction, got: %v", err)
	}
}

func TestBuildLocalAndPullRequiresRegistry(t *testing.T) {
	cfg := testConfig()
	cfg.Build.Strategy = "local" // no registry
	// Validation must happen before any docker call.
	err := buildLocalAndPull(context.Background(), cfg, &models.Application{Config: cfg}, core.NewLocalExecutor(), t.TempDir(), nil)
	if err == nil {
		t.Fatalf("local strategy without registry must fail")
	}
}

func TestTeardownRoutesToDriver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "easydrop.toml")
	body := `
[app]
name = "my-api"
port = 8080
[server]
host = "localhost"
user = "root"
[driver]
type = "single"
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	// Single driver teardown is idempotent: nothing deployed -> nil error,
	// and it must not require the config's app to exist on the host.
	if _, err := Teardown(context.Background(), path, "", TeardownOptions{}); err != nil {
		t.Errorf("Teardown() on a clean host must be nil, got: %v", err)
	}
}

// feedLines pushes lines into a channel and closes it, mimicking a driver
// that has finished streaming.
func feedLines(lines ...string) <-chan string {
	ch := make(chan string, len(lines))
	for _, l := range lines {
		ch <- l
	}
	close(ch)
	return ch
}

func TestCompileLogFilter(t *testing.T) {
	for _, tc := range []struct {
		expr, wantKept, wantDropped string
	}{
		{"", "anything at all", ""},
		{"error", "ERROR: boom", "all good"},
		{"error|panic", "a PANIC here", "a warning"},
		{"^FATAL", "FATAL: db gone", "not FATAL: false"},
	} {
		re, err := compileLogFilter(tc.expr)
		if err != nil {
			t.Fatalf("compileLogFilter(%q): %v", tc.expr, err)
		}
		if !re.MatchString(tc.wantKept) {
			t.Errorf("filter %q must keep %q", tc.expr, tc.wantKept)
		}
		if tc.wantDropped != "" && re.MatchString(tc.wantDropped) {
			t.Errorf("filter %q must drop %q", tc.expr, tc.wantDropped)
		}
	}
	// An invalid expression is an error, not a filter that silently passes
	// everything through – the latter would read as "the app logged nothing".
	if _, err := compileLogFilter("error("); err == nil {
		t.Fatalf("invalid regexp must be rejected")
	} else if !strings.Contains(err.Error(), "invalid log filter") {
		t.Errorf("error must name the problem, got: %v", err)
	}
}

func TestCollectLogsFiltersAndCounts(t *testing.T) {
	re, err := compileLogFilter("error|fatal")
	if err != nil {
		t.Fatal(err)
	}
	res := collectLogs(feedLines(
		"starting up",
		"ERROR: db unreachable",
		"retrying",
		"FATAL: gave up",
		"exiting",
	), re, 0)
	if got, want := res.Scanned, 5; got != want {
		t.Errorf("Scanned = %d, want %d", got, want)
	}
	if got, want := res.Matched, 2; got != want {
		t.Errorf("Matched = %d, want %d", got, want)
	}
	if len(res.Lines) != 2 || !strings.Contains(res.Lines[0], "db unreachable") ||
		!strings.Contains(res.Lines[1], "gave up") {
		t.Errorf("filtered lines wrong: %v", res.Lines)
	}
}

func TestCollectLogsLimitCountsKeptLines(t *testing.T) {
	re, _ := compileLogFilter("error")
	// The limit bounds what is *returned*, so a snapshot stays small even when
	// it scans a wide window.
	res := collectLogs(feedLines("error 1", "noise", "error 2", "error 3"), re, 2)
	if len(res.Lines) != 2 || res.Matched != 2 {
		t.Errorf("limit must cap kept lines: %+v", res)
	}
	if res.Scanned != 3 {
		t.Errorf("Scanned = %d, want 3 (collection stops once the limit is hit)", res.Scanned)
	}
}

func TestCollectLogsNoFilterKeepsEverything(t *testing.T) {
	re, _ := compileLogFilter("")
	res := collectLogs(feedLines("a", "b", "c"), re, 0)
	if res.Scanned != 3 || res.Matched != 3 || len(res.Lines) != 3 {
		t.Errorf("empty filter must be a pass-through: %+v", res)
	}
}

func TestCollectLogsEmptyResultIsDistinguishable(t *testing.T) {
	re, _ := compileLogFilter("fatal")
	res := collectLogs(feedLines("all fine", "still fine"), re, 0)
	// The distinction matters: Scanned > 0 with Matched == 0 means "the filter
	// missed", not "the container was silent".
	if res.Scanned != 2 || res.Matched != 0 || len(res.Lines) != 0 {
		t.Errorf("want 2 scanned / 0 matched, got %+v", res)
	}
}

// teardownConfig writes a config with the given nginx block body.
func teardownConfig(t *testing.T, nginx string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "easydrop.toml")
	body := "[app]\nname = \"my-api\"\nport = 8080\n[server]\nhost = \"localhost\"\n" +
		"user = \"root\"\n[driver]\ntype = \"single\"\n" + nginx
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTeardownWithoutDomainLeavesIngressAlone(t *testing.T) {
	// No domain means easydrop never wrote a vhost, so there is nothing to
	// remove and nothing to reload – a localhost deploy without ingress must not
	// start shelling out to nginx.
	path := teardownConfig(t, "")
	res, err := Teardown(context.Background(), path, "", TeardownOptions{})
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if res.IngressRemoved {
		t.Errorf("nothing could have been removed: %+v", res)
	}
	if res.IngressNote == "" {
		t.Error("the result must explain why ingress was skipped")
	}
}

func TestTeardownRefusesConfigWithoutPort(t *testing.T) {
	// Validation happens before any teardown work: a config that cannot deploy
	// cannot tear down either, and half-running a teardown on a bad config would
	// be worse than refusing.
	dir := t.TempDir()
	path := filepath.Join(dir, "easydrop.toml")
	if err := os.WriteFile(path, []byte("[app]\nname = \"my-api\"\n[server]\nhost = \"localhost\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Teardown(context.Background(), path, "", TeardownOptions{}); err == nil {
		t.Error("a config without app.port must be refused")
	}
}

func TestAppendNote(t *testing.T) {
	for _, tc := range []struct{ in, add, want string }{
		{"", "one", "one"},
		{"one", "two", "one; two"},
	} {
		if got := appendNote(tc.in, tc.add); got != tc.want {
			t.Errorf("appendNote(%q, %q) = %q, want %q", tc.in, tc.add, got, tc.want)
		}
	}
}
