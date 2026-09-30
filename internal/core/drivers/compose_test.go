package drivers

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"easydrop/internal/models"
)

func composeApp() *models.Application {
	return &models.Application{Config: &models.Config{
		App:    models.AppConfig{Name: "my-web", Port: 8080, HealthCheckPath: "/"},
		Driver: models.DriverConfig{Type: "compose", ComposeFile: "docker-compose.yml"},
	}}
}

func composeSrc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir+"/docker-compose.yml", "services:\n  web:\n    image: nginx:alpine\n")
	writeFile(t, dir+"/Dockerfile", "FROM scratch\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestComposeDeployFirstTime(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker info --format", okResp(""))
	f.on("tar -xzf", okResp(""))
	f.on("docker compose -p 'easydrop-my-web'", okResp(""), okResp("web|running\n"))
	f.on("mkdir -p", okResp(""))
	f.on("test -f '/home/test/.easydrop/apps/my-web/compose.yml'", errResp()) // no live yet
	f.on("cp -f '/tmp/easydrop/builds/my-web/docker-compose.yml'", okResp(""))
	f.on("test -f '/tmp/easydrop/builds/my-web/.env'", okResp(""))
	f.on("rm -rf '/tmp/easydrop/builds/my-web'", okResp(""))

	d := NewComposeDriver(f)
	d.SrcDir = composeSrc(t)
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), composeApp()); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("up -d --build --remove-orphans") {
		t.Errorf("up must build on host, ran: %v", f.calls)
	}
	if f.ran("compose.previous.yml") {
		t.Errorf("no backup expected on first deploy, ran: %v", f.calls)
	}
}

func TestComposeDeployReplacesLive(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker info --format", okResp(""))
	f.on("tar -xzf", okResp(""))
	f.on("docker compose -p 'easydrop-my-web'", okResp(""), okResp("web|running\n"))
	f.on("mkdir -p", okResp(""))
	f.on("test -f '/home/test/.easydrop/apps/my-web/compose.yml'", okResp(""))
	f.on("cp -f '/home/test/.easydrop/apps/my-web/compose.yml' '/home/test/.easydrop/apps/my-web/compose.previous.yml'", okResp(""))
	f.on("cp -f '/tmp/easydrop/builds/my-web/docker-compose.yml'", okResp(""))
	f.on("test -f '/tmp/easydrop/builds/my-web/.env'", okResp(""))
	f.on("rm -rf '/tmp/easydrop/builds/my-web'", okResp(""))

	d := NewComposeDriver(f)
	d.SrcDir = composeSrc(t)
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), composeApp()); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("compose.previous.yml") {
		t.Errorf("live file must be backed up, ran: %v", f.calls)
	}
}

func TestComposeDeployUnhealthyFails(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker info --format", okResp(""))
	f.on("tar -xzf", okResp(""))
	f.on("docker compose -p 'easydrop-my-web'", okResp(""), okResp("web|restarting\n"))

	d := NewComposeDriver(f)
	d.SrcDir = composeSrc(t)
	d.Out = &bytes.Buffer{}
	err := d.Deploy(context.Background(), composeApp())
	if err == nil {
		t.Fatalf("Deploy() expected unhealthy error, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error should name bad states, got: %v", err)
	}
	if f.ran("mkdir -p") || f.ran("rm -rf") {
		t.Errorf("failed deploy must not persist or purge, ran: %v", f.calls)
	}
}

func TestComposeRollback(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-web/compose.previous.yml'", okResp(""))
	f.on("cp -f '/home/test/.easydrop/apps/my-web/compose.previous.yml'", okResp(""))
	f.on("docker compose -p 'easydrop-my-web'", okResp(""), okResp("web|running\n"))
	f.on("rm -f '/home/test/.easydrop/apps/my-web/compose.previous.yml'", okResp(""))

	d := NewComposeDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Rollback(context.Background(), composeApp()); err != nil {
		t.Fatalf("Rollback() unexpected error: %v", err)
	}
	if f.ran("--build") {
		t.Errorf("rollback must reuse cached images (no --build), ran: %v", f.calls)
	}
}

func TestComposeRollbackNoBackup(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-web/compose.previous.yml'", errResp())

	d := NewComposeDriver(f)
	d.Out = &bytes.Buffer{}
	err := d.Rollback(context.Background(), composeApp())
	if err == nil {
		t.Fatalf("Rollback() expected no-backup error, got nil")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "cp ") || strings.HasPrefix(c, "rm ") || strings.HasPrefix(c, "mkdir ") ||
			(strings.HasPrefix(c, "docker ") && !strings.HasPrefix(c, "docker info")) {
			t.Errorf("no mutations without backup, ran: %v", f.calls)
			break
		}
	}
}

func TestComposeStatus(t *testing.T) {
	cases := []struct {
		name       string
		fileOk     bool
		psOut      string
		wantStatus string
	}{
		{"missing", false, "", "Down"},
		{"running", true, "web|running|Up 5 minutes", "Up"},
		{"restarting", true, "web|restarting|Restarting (1) 3 seconds ago", "Restarting"},
		{"exited", true, "web|exited|Exited (1) 2 hours ago", "Down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &queueFake{}
			f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
			f.on("docker info --format '{{.DockerRootDir}}", errResp())
			if tc.fileOk {
				f.on("test -f '/home/test/.easydrop/apps/my-web/compose.yml'", okResp(""))
				f.on("docker compose -p 'easydrop-my-web'", okResp(tc.psOut))
			} else {
				f.on("test -f '/home/test/.easydrop/apps/my-web/compose.yml'", errResp())
			}
			d := NewComposeDriver(f)
			st, err := d.Status(context.Background(), "my-web")
			if err != nil {
				t.Fatalf("Status() unexpected error: %v", err)
			}
			if st.Status != tc.wantStatus {
				t.Errorf("Status() = %q, want %q", st.Status, tc.wantStatus)
			}
		})
	}
}

func TestComposeLogs(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-web/compose.yml'", okResp(""))
	f.on("docker compose -p 'easydrop-my-web'", okResp("web-1 | hello\n"))
	d := NewComposeDriver(f)
	ch, err := d.Logs(context.Background(), "my-web", 100, false)
	if err != nil {
		t.Fatalf("Logs() unexpected error: %v", err)
	}
	var got []string
	for l := range ch {
		got = append(got, l)
	}
	if len(got) != 1 || !strings.Contains(got[0], "hello") {
		t.Errorf("Logs() = %v", got)
	}
}

func TestComposeTeardown(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-web/compose.yml'", okResp(""))
	f.on("docker compose -p 'easydrop-my-web'", okResp(""))
	f.on("rm -rf '/home/test/.easydrop/apps/my-web'", okResp(""))
	d := NewComposeDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Teardown(context.Background(), "my-web"); err != nil {
		t.Fatalf("Teardown() unexpected error: %v", err)
	}
	if f.ran("-v") {
		t.Errorf("volumes must be kept, ran: %v", f.calls)
	}
}

func TestComposeTeardownIdempotent(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-web/compose.yml'", errResp())
	f.on("rm -rf '/home/test/.easydrop/apps/my-web'", okResp(""))
	d := NewComposeDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Teardown(context.Background(), "my-web"); err != nil {
		t.Fatalf("Teardown() on empty host must be nil, got: %v", err)
	}
	if f.ran("docker compose") {
		t.Errorf("no compose calls when nothing deployed, ran: %v", f.calls)
	}
}
