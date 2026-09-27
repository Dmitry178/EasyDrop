package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easydrop/internal/models"
)

// fakeBuildExecutor scripts ExecCommand and captures UploadFile payloads.
type fakeBuildExecutor struct {
	script  map[string]error
	calls   []string
	uploads map[string][]byte
}

func (f *fakeBuildExecutor) ExecCommand(_ context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	if err, ok := f.script[cmd]; ok {
		if err != nil {
			return "", "fake stderr", 1, err
		}
		return "", "", 0, nil
	}
	return "", "", -1, fmt.Errorf("fake: unexpected command %q", cmd)
}

func (f *fakeBuildExecutor) UploadFile(_ context.Context, srcPath, destPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
	}
	f.uploads[destPath] = data
	return nil
}

func (f *fakeBuildExecutor) Close() error { return nil }

func (f *fakeBuildExecutor) ranPrefix(prefix string) (string, bool) {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return c, true
		}
	}
	return "", false
}

func testApp(name string, noCache bool) *models.Application {
	return &models.Application{Config: &models.Config{
		App:   models.AppConfig{Name: name, Port: 8080},
		Build: models.BuildConfig{Strategy: "remote", NoCache: noCache},
	}}
}

func fixtureSrc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"Dockerfile": "FROM scratch\n",
		"main.go":    "package main\n",
	} {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// uploadedNames extracts tar entry names from an uploaded blob.
func uploadedNames(t *testing.T, blob []byte) []string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("uploaded blob is not gzip: %v", err)
	}
	defer gr.Close()
	var names []string
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestBuildHappyPath(t *testing.T) {
	f := &fakeBuildExecutor{script: map[string]error{
		"tar -xzf '/tmp/easydrop/builds/my-api/project.tar.gz' -C '/tmp/easydrop/builds/my-api'": nil,
		"docker build -t 'easydrop/my-api:latest'  '/tmp/easydrop/builds/my-api'":                nil,
		"rm -rf '/tmp/easydrop/builds/my-api'":                                                   nil,
	}}
	b := NewRemoteBuilder(f)
	b.SrcDir = fixtureSrc(t)
	var logBuf bytes.Buffer
	b.Out = &logBuf

	if err := b.Build(context.Background(), testApp("my-api", false)); err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}
	blob, ok := f.uploads["/tmp/easydrop/builds/my-api/project.tar.gz"]
	if !ok {
		t.Fatalf("expected upload to /tmp/easydrop/builds/my-api/project.tar.gz, got %v", f.uploads)
	}
	names := uploadedNames(t, blob)
	if len(names) == 0 || !strings.Contains(strings.Join(names, ","), "Dockerfile") {
		t.Errorf("uploaded archive must contain Dockerfile, got %v", names)
	}
	if !strings.Contains(logBuf.String(), "my-api") {
		t.Errorf("expected progress logs, got:\n%s", logBuf.String())
	}
}

func TestBuildNoCacheFlag(t *testing.T) {
	f := &fakeBuildExecutor{script: map[string]error{
		"tar -xzf '/tmp/easydrop/builds/my-api/project.tar.gz' -C '/tmp/easydrop/builds/my-api'": nil,
		"docker build -t 'easydrop/my-api:latest' --no-cache '/tmp/easydrop/builds/my-api'":      nil,
		"rm -rf '/tmp/easydrop/builds/my-api'":                                                   nil,
	}}
	b := NewRemoteBuilder(f)
	b.SrcDir = fixtureSrc(t)
	b.Out = &bytes.Buffer{}
	if err := b.Build(context.Background(), testApp("my-api", true)); err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}
}

func TestBuildFailureKeepsStaging(t *testing.T) {
	f := &fakeBuildExecutor{script: map[string]error{
		"tar -xzf '/tmp/easydrop/builds/my-api/project.tar.gz' -C '/tmp/easydrop/builds/my-api'": nil,
		"docker build -t 'easydrop/my-api:latest'  '/tmp/easydrop/builds/my-api'":                fmt.Errorf("fake: docker build exited 1"),
	}}
	b := NewRemoteBuilder(f)
	b.SrcDir = fixtureSrc(t)
	b.Out = &bytes.Buffer{}
	err := b.Build(context.Background(), testApp("my-api", false))
	if err == nil {
		t.Fatalf("Build() expected error on docker failure, got nil")
	}
	if cmd, found := f.ranPrefix("rm -rf"); found {
		t.Errorf("staging must be kept for debugging on failure, but ran %q", cmd)
	}
}

func TestBuildStagingBaseOverride(t *testing.T) {
	t.Setenv("EASYDROP_STAGING_BASE", "/srv/easydrop")
	f := &fakeBuildExecutor{script: map[string]error{
		"tar -xzf '/srv/easydrop/builds/my-api/project.tar.gz' -C '/srv/easydrop/builds/my-api'": nil,
		"docker build -t 'easydrop/my-api:latest'  '/srv/easydrop/builds/my-api'":                nil,
		"rm -rf '/srv/easydrop/builds/my-api'":                                                   nil,
	}}
	b := NewRemoteBuilder(f)
	b.SrcDir = fixtureSrc(t)
	b.Out = &bytes.Buffer{}
	if err := b.Build(context.Background(), testApp("my-api", false)); err != nil {
		t.Fatalf("Build() unexpected error: %v", err)
	}
	if _, ok := f.uploads["/srv/easydrop/builds/my-api/project.tar.gz"]; !ok {
		t.Errorf("expected upload under override base, got %v", f.uploads)
	}
}

func TestBuildInvalidAppName(t *testing.T) {
	for _, name := range []string{"", "My-API", "my api", "a/b", "-lead", "UPPER"} {
		f := &fakeBuildExecutor{script: map[string]error{}}
		b := NewRemoteBuilder(f)
		b.SrcDir = fixtureSrc(t)
		b.Out = &bytes.Buffer{}
		if err := b.Build(context.Background(), testApp(name, false)); err == nil {
			t.Errorf("Build() expected error for app name %q, got nil", name)
		}
		if len(f.calls) != 0 {
			t.Errorf("no host commands must run for invalid name %q, ran %v", name, f.calls)
		}
	}
}

func TestBuildNilApp(t *testing.T) {
	b := NewRemoteBuilder(&fakeBuildExecutor{script: map[string]error{}})
	b.Out = &bytes.Buffer{}
	if err := b.Build(context.Background(), nil); err == nil {
		t.Errorf("Build(nil) expected error, got nil")
	}
}
