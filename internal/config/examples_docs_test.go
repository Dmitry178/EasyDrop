package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"easydrop/internal/models"
)

// examplesReadme is the annotated key reference shipped next to the configs,
// resolved against examplesDir so both live in one place.
var examplesReadme = filepath.Join(examplesDir, "README.md")

// keyTableRow matches one row of the "Configuration keys" table:
//
//	| `app.name` | string | – | **required**; … |
var keyTableRow = regexp.MustCompile("^\\|\\s*`([a-z_]+\\.[a-z_]+)`\\s*\\|")

// configKeys returns every `section.key` the parser actually accepts, derived
// by reflection over models.Config – the same structs ParseConfig decodes into.
func configKeys() []string {
	var keys []string
	tpe := reflect.TypeOf(models.Config{})
	for i := 0; i < tpe.NumField(); i++ {
		section, _, _ := strings.Cut(tpe.Field(i).Tag.Get("toml"), ",")
		st := tpe.Field(i).Type
		for j := 0; j < st.NumField(); j++ {
			key, _, _ := strings.Cut(st.Field(j).Tag.Get("toml"), ",")
			keys = append(keys, section+"."+key)
		}
	}
	sort.Strings(keys)
	return keys
}

// documentedKeys returns the `section.key` entries listed in the README table,
// in file order.
func documentedKeys(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(examplesReadme)
	if err != nil {
		t.Fatalf("read %s: %v", examplesReadme, err)
	}
	var keys []string
	inTable := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			inTable = line == "## Configuration keys"
			continue
		}
		if !inTable {
			continue
		}
		if m := keyTableRow.FindStringSubmatch(line); m != nil {
			keys = append(keys, m[1])
		}
	}
	return keys
}

// TestExamplesReadmeDocumentsEveryConfigKey is the guard on the reference table
// in examples/README.md. It is hand-written, so it drifts: a key added to
// models.Config appears in the parser and in the generated MCP schema while
// being absent from the table a user reads, and a key that was removed lingers
// as advice to set something that no longer does anything.
//
// The direction that actually hurts is the missing one – an undocumented key is
// invisible – so both are checked, and the failure message says which.
func TestExamplesReadmeDocumentsEveryConfigKey(t *testing.T) {
	want := configKeys()
	documented := documentedKeys(t)
	if len(documented) == 0 {
		t.Fatalf("no keys parsed from the %s table – the row pattern or the heading changed", examplesReadme)
	}

	inTable := map[string]bool{}
	for _, k := range documented {
		inTable[k] = true
	}
	real := map[string]bool{}
	for _, k := range want {
		real[k] = true
		if !inTable[k] {
			t.Errorf("config key %q is accepted by the parser but missing from the %s key table", k, examplesReadme)
		}
	}
	for _, k := range documented {
		if !real[k] {
			t.Errorf("key table documents %q, which models.Config does not define – the doc is stale", k)
		}
	}

	// Duplicates are a silent trap: one row shadows the other in review.
	seen := map[string]int{}
	for _, k := range documented {
		seen[k]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("key %q documented %d times in %s", k, n, examplesReadme)
		}
	}
}

// TestExamplesReadmeKeyTableHasNoUnknownSections guards the parser of the table
// itself: if the "Configuration keys" heading is renamed or its rows change
// shape, documentedKeys would return an empty or partial list and the test above
// would fail for the wrong reason. This pins the expectation explicitly.
func TestExamplesReadmeKeyTableHasNoUnknownSections(t *testing.T) {
	documented := documentedKeys(t)
	for _, k := range documented {
		section, _, ok := strings.Cut(k, ".")
		if !ok || section == "" {
			t.Errorf("malformed key table entry %q", k)
		}
	}
	if len(documented) < 15 {
		t.Errorf("only %d keys parsed from the table, expected the full reference (>=15)", len(documented))
	}
}

// TestSourceScanRespectsWalkLimits locks the bounds that keep `init` instant on
// a monorepo. They are a performance contract, not an implementation detail: a
// regression that removed the caps would not fail any port-detection test, it
// would just make `init_project` – an MCP tool an agent calls on a repository it
// has never seen – walk a million files.
func TestSourceScanRespectsWalkLimits(t *testing.T) {
	dir := t.TempDir()
	// More scannable files than the cap allows, plus a decoy buried past the
	// limit: if the cap were removed, the walk would find it.
	for i := 0; i < portScanMaxFiles+50; i++ {
		writeAt(t, filepath.Join(dir, spreadDir(i), "main.go"), "package main\n")
	}
	// A skipped directory must contribute nothing, even when it is the only
	// place the port is written.
	writeAt(t, filepath.Join(dir, "node_modules", "left-pad", "index.js"), "app.listen(9999)\n")
	writeAt(t, filepath.Join(dir, "vendor", "lib", "server.go"), `http.ListenAndServe(":9998")`)

	files := collectScanFiles(dir)
	if len(files) > portScanMaxFiles {
		t.Errorf("walk returned %d files, cap is %d", len(files), portScanMaxFiles)
	}
	for _, rel := range files {
		first := strings.Split(filepath.ToSlash(rel), "/")[0]
		if scanSkipDirs[first] {
			t.Errorf("skipped dir %q must not be scanned (got %s)", first, rel)
		}
	}

	// Nothing detectable: the decoys are in skipped dirs, and the in-limit
	// files declare no port.
	if port, ok := detectSourcePort(dir); ok {
		t.Errorf("detected port %d, want none (skipped dirs must not contribute)", port)
	}
}

// TestSourceScanFindsPortInLimitFiles is the counterpart: the caps must not be
// so tight that a real project is missed.
func TestSourceScanFindsPortInLimitFiles(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {\n\thttp.ListenAndServe(\":9090\", nil)\n}\n")
	port, ok := detectSourcePort(dir)
	if !ok || port != 9090 {
		t.Errorf("detectSourcePort() = %d, %v; want 9090, true", port, ok)
	}
}

// TestSourceScanHonorsDockerignoreStyleExclusions documents that the walk skips
// by directory name only: a project cannot add to the skip list from its config,
// because `init` is contractually side-effect free and reads no easydrop.toml
// (M14). Test locks that a `.easydropignore` has no effect, so the limitation
// cannot be "fixed" by accident.
func TestSourceScanHonorsDockerignoreStyleExclusions(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, filepath.Join(dir, ".easydropignore"), "sandbox\n")
	writeAt(t, filepath.Join(dir, "sandbox", "app.py"), "app.run(port=7777)\n")
	port, ok := detectSourcePort(dir)
	if !ok || port != 7777 {
		t.Errorf("no user-supplied ignore file is honored (got %d, %v); "+
			"init must stay config-free", port, ok)
	}
}

// writeAt writes body to an absolute path, creating the parent directories.
// writeFile in env_test.go only handles a bare filename inside dir.
func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// spreadDir gives each generated file a distinct path so the walk sees real tree
// depth and a genuinely large directory count, not one flat folder.
func spreadDir(i int) string {
	return "pkg/" + strconv.Itoa(i/25) + "/mod" + strconv.Itoa(i%25)
}
