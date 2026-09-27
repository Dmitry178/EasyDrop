package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeFixture creates files (rel path → content) under a temp dir.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

// listArchive returns sorted entry names from a tar.gz byte blob.
func listArchive(t *testing.T, blob []byte) []string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		names = append(names, hdr.Name)
	}
	sort.Strings(names)
	return names
}

func archiveToBytes(t *testing.T, srcDir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := CreateProjectArchive(srcDir, &buf); err != nil {
		t.Fatalf("CreateProjectArchive(): %v", err)
	}
	return buf.Bytes()
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestArchiveDefaults(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"Dockerfile":            "FROM scratch",
		"main.go":               "package main",
		"sub/app.js":            "console.log(1)",
		".git/config":           "[core]",
		".git/refs/heads/main":  "abc",
		"node_modules/dep/x.js": "x",
		"project.tar.gz":        "stale bundle",
		"sub/project.tar.gz":    "nested bundle (basename rule)",
	})
	names := listArchive(t, archiveToBytes(t, dir))

	for _, want := range []string{"Dockerfile", "main.go", "sub/app.js"} {
		if !contains(names, want) {
			t.Errorf("archive missing %q, got %v", want, names)
		}
	}
	for _, n := range names {
		if n == ".git/config" || n == "node_modules/dep/x.js" || n == "project.tar.gz" || n == "sub/project.tar.gz" {
			t.Errorf("archive must exclude %q, got %v", n, names)
		}
	}
}

func TestArchiveDockerignore(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"Dockerfile":    "FROM scratch",
		"main.go":       "package main",
		"secret.env":    "TOKEN=x",
		"keep.env":      "OK=1",
		"dist/app.bin":  "bin",
		"dist/keep.txt": "keep",
		"docs/a.md":     "a",
		".dockerignore": "# comment\n\n*.env\n!keep.env\ndist/*\n!dist/keep.txt\n*.md\n",
	})
	names := listArchive(t, archiveToBytes(t, dir))

	for _, want := range []string{"Dockerfile", "main.go", "keep.env", "dist/keep.txt"} {
		if !contains(names, want) {
			t.Errorf("archive missing %q (! negation / dir-scoped keep must survive), got %v", want, names)
		}
	}
	for _, gone := range []string{"secret.env", "dist/app.bin", "docs/a.md"} {
		if contains(names, gone) {
			t.Errorf("archive must exclude %q, got %v", gone, names)
		}
	}
}

func TestArchiveDockerignoreDoubleStar(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"a/test.log":    "x",
		"a/b/test.log":  "x",
		"a/b/keep.go":   "x",
		".dockerignore": "**/test.log\n",
	})
	names := listArchive(t, archiveToBytes(t, dir))
	if !contains(names, "a/b/keep.go") {
		t.Errorf("archive missing a/b/keep.go, got %v", names)
	}
	if contains(names, "a/test.log") || contains(names, "a/b/test.log") {
		t.Errorf("** pattern must exclude nested logs, got %v", names)
	}
}

func TestArchiveMissingDir(t *testing.T) {
	var buf bytes.Buffer
	if err := CreateProjectArchive(filepath.Join(t.TempDir(), "nope"), &buf); err == nil {
		t.Errorf("CreateProjectArchive() expected error for missing dir, got nil")
	}
}

func TestArchivePreservesModes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	blob := archiveToBytes(t, dir)

	gr, _ := gzip.NewReader(bytes.NewReader(blob))
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "run.sh" && hdr.FileInfo().Mode().Perm() != 0755 {
			t.Errorf("run.sh mode = %o, want 755", hdr.FileInfo().Mode().Perm())
		}
	}
}
