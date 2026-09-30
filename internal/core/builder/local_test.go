package builder

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"easydrop/internal/models"
)

func localApp(name, registry, image string, noCache bool) *models.Application {
	return &models.Application{Config: &models.Config{
		App:   models.AppConfig{Name: name, Port: 8080, HostPort: 8080},
		Build: models.BuildConfig{Strategy: "local", Registry: registry, Image: image, NoCache: noCache},
	}}
}

func TestTargetRef(t *testing.T) {
	cases := []struct {
		name     string
		registry string
		image    string
		appName  string
		want     string
		wantErr  string
	}{
		{"default image is app name", "ghcr.io", "", "my-api", "ghcr.io/my-api:latest", ""},
		{"namespace in registry", "registry.example.com/myorg", "", "my-api", "registry.example.com/myorg/my-api:latest", ""},
		{"custom image with tag", "ghcr.io", "web:2.1", "my-api", "ghcr.io/web:2.1", ""},
		{"custom image namespaced", "ghcr.io", "team/web:2.1", "my-api", "ghcr.io/team/web:2.1", ""},
		{"registry with port", "localhost:5000", "", "my-api", "localhost:5000/my-api:latest", ""},
		{"trailing slash trimmed", "ghcr.io/", "", "my-api", "ghcr.io/my-api:latest", ""},
		{"missing registry", "", "", "my-api", "", "requires build.registry"},
		{"scheme rejected", "https://ghcr.io", "", "my-api", "", "must not contain a scheme"},
		{"invalid registry", "not a registry", "", "my-api", "", "invalid build.registry"},
		{"empty image", "ghcr.io", ":v1", "my-api", "", "repository name is empty"},
		{"invalid image name", "ghcr.io", "Bad Name", "my-api", "", "invalid build.image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TargetRef(localApp(tc.appName, tc.registry, tc.image, false).Config)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("TargetRef() expected error %q, got %q", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("TargetRef() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("TargetRef() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLocalBuilderValidatesBeforeTouchingDocker(t *testing.T) {
	b := NewLocalBuilder()
	b.Out = &bytes.Buffer{}
	b.SrcDir = t.TempDir()
	// No registry configured: must fail without invoking docker.
	if _, err := b.BuildAndPush(context.Background(), localApp("my-api", "", "", false)); err == nil {
		t.Errorf("BuildAndPush() without registry must fail")
	}
	if _, err := b.BuildAndPush(context.Background(), nil); err == nil {
		t.Errorf("BuildAndPush(nil) must fail")
	}
	if _, err := b.BuildAndPush(context.Background(), localApp("BAD NAME", "ghcr.io", "", false)); err == nil {
		t.Errorf("BuildAndPush() with invalid app name must fail")
	}
}
