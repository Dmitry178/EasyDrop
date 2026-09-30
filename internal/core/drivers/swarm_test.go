package drivers

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"easydrop/internal/models"
)

func swarmApp() *models.Application {
	return &models.Application{Config: &models.Config{
		App:    models.AppConfig{Name: "my-cluster", Port: 8080, HealthCheckPath: "/"},
		Driver: models.DriverConfig{Type: "swarm", ComposeFile: "docker-compose.yml"},
	}}
}

func swarmSrc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir+"/docker-compose.yml", "services:\n  web:\n    image: nginx:alpine\n")
	return dir
}

func TestSwarmDeployInitsAndShips(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker info --format '{{.Swarm.LocalNodeState}}'", okResp("inactive\n"))
	f.on("docker swarm init", okResp(""))
	f.on("docker info --format '{{.DockerRootDir}}", okResp(""))
	f.on("tar -xzf", okResp(""))
	f.on("docker compose -f '/tmp/easydrop/builds/my-cluster/docker-compose.yml' build", okResp(""))
	f.on("docker stack deploy -c '/tmp/easydrop/builds/my-cluster/docker-compose.yml' 'easydrop-my-cluster'", okResp(""))
	f.on("docker stack services 'easydrop-my-cluster' --format '{{.Replicas}}'", okResp("1/1\n"))
	f.on("mkdir -p '/home/test/.easydrop/apps/my-cluster'", okResp(""))
	f.on("test -f '/home/test/.easydrop/apps/my-cluster/compose.yml'", errResp())
	f.on("cp -f '/tmp/easydrop/builds/my-cluster/docker-compose.yml' '/home/test/.easydrop/apps/my-cluster/compose.yml'", okResp(""))
	f.on("rm -rf '/tmp/easydrop/builds/my-cluster'", okResp(""))

	d := NewSwarmDriver(f)
	d.SrcDir = swarmSrc(t)
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), swarmApp()); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("docker swarm init") {
		t.Errorf("inactive node must be initialized, ran: %v", f.calls)
	}
}

func TestSwarmDeploySkipsInitWhenActive(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker info --format '{{.Swarm.LocalNodeState}}'", okResp("active\n"))
	f.on("docker info --format '{{.DockerRootDir}}", okResp(""))
	f.on("tar -xzf", okResp(""))
	f.on("docker compose -f", okResp(""))
	f.on("docker stack deploy", okResp(""))
	f.on("docker stack services 'easydrop-my-cluster' --format '{{.Replicas}}'", okResp("2/2\n"))
	f.on("mkdir -p", okResp(""))
	f.on("test -f '/home/test/.easydrop/apps/my-cluster/compose.yml'", okResp(""))
	f.on("cp -f '/home/test/.easydrop/apps/my-cluster/compose.yml' '/home/test/.easydrop/apps/my-cluster/compose.previous.yml'", okResp(""))
	f.on("cp -f '/tmp/easydrop/builds/my-cluster/docker-compose.yml'", okResp(""))
	f.on("rm -rf", okResp(""))

	d := NewSwarmDriver(f)
	d.SrcDir = swarmSrc(t)
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), swarmApp()); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if f.ran("docker swarm init") {
		t.Errorf("active swarm must not re-init, ran: %v", f.calls)
	}
	if !f.ran("compose.previous.yml") {
		t.Errorf("live stack file must be backed up, ran: %v", f.calls)
	}
}

func TestSwarmDeployDegradedFails(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker info --format '{{.Swarm.LocalNodeState}}'", okResp("active\n"))
	f.on("docker info --format '{{.DockerRootDir}}", okResp(""))
	f.on("tar -xzf", okResp(""))
	f.on("docker compose -f", okResp(""))
	f.on("docker stack deploy", okResp(""))
	f.on("docker stack services 'easydrop-my-cluster' --format '{{.Replicas}}'", okResp("0/1\n"))

	d := NewSwarmDriver(f)
	d.SrcDir = swarmSrc(t)
	d.Out = &bytes.Buffer{}
	err := d.Deploy(context.Background(), swarmApp())
	if err == nil {
		t.Fatalf("Deploy() expected replica error, got nil")
	}
	if f.ran("mkdir -p") {
		t.Errorf("failed deploy must not persist, ran: %v", f.calls)
	}
}

func TestSwarmStatus(t *testing.T) {
	cases := []struct {
		name       string
		fileOk     bool
		replicas   string
		wantStatus string
	}{
		{"missing", false, "", "Down"},
		{"healthy", true, "1/1", "Up"},
		{"scaled", true, "3/3", "Up"},
		{"degraded", true, "1/3", "Down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &queueFake{}
			f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
			f.on("docker info --format '{{.DockerRootDir}}", errResp())
			if tc.fileOk {
				f.on("test -f '/home/test/.easydrop/apps/my-cluster/compose.yml'", okResp(""))
				f.on("docker stack services 'easydrop-my-cluster' --format '{{.Replicas}}'", okResp(tc.replicas))
			} else {
				f.on("test -f '/home/test/.easydrop/apps/my-cluster/compose.yml'", errResp())
			}
			d := NewSwarmDriver(f)
			st, err := d.Status(context.Background(), "my-cluster")
			if err != nil {
				t.Fatalf("Status() unexpected error: %v", err)
			}
			if st.Status != tc.wantStatus {
				t.Errorf("Status() = %q, want %q", st.Status, tc.wantStatus)
			}
		})
	}
}

func TestSwarmLogsAcrossServices(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-cluster/compose.yml'", okResp(""))
	f.on("docker stack services 'easydrop-my-cluster' --format '{{.Name}}'", okResp("easydrop-my-cluster_web\n"))
	f.on("docker service logs --tail 100 'easydrop-my-cluster_web'", okResp("hello\n"))
	d := NewSwarmDriver(f)
	ch, err := d.Logs(context.Background(), "my-cluster", 100, false)
	if err != nil {
		t.Fatalf("Logs() unexpected error: %v", err)
	}
	var got []string
	for l := range ch {
		got = append(got, l)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "easydrop-my-cluster_web") {
		t.Errorf("Logs() must prefix service logs, got %v", got)
	}
}

func TestSwarmRollback(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-cluster/compose.previous.yml'", okResp(""))
	f.on("cp -f '/home/test/.easydrop/apps/my-cluster/compose.previous.yml'", okResp(""))
	f.on("docker stack deploy -c '/home/test/.easydrop/apps/my-cluster/compose.yml' 'easydrop-my-cluster'", okResp(""))
	f.on("docker stack services 'easydrop-my-cluster' --format '{{.Replicas}}'", okResp("1/1\n"))
	f.on("rm -f '/home/test/.easydrop/apps/my-cluster/compose.previous.yml'", okResp(""))

	d := NewSwarmDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Rollback(context.Background(), swarmApp()); err != nil {
		t.Fatalf("Rollback() unexpected error: %v", err)
	}
}

func TestSwarmRollbackNoBackup(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("test -f '/home/test/.easydrop/apps/my-cluster/compose.previous.yml'", errResp())
	d := NewSwarmDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Rollback(context.Background(), swarmApp()); err == nil {
		t.Fatalf("Rollback() expected no-backup error, got nil")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "cp ") || strings.HasPrefix(c, "rm ") ||
			(strings.HasPrefix(c, "docker ") && !strings.HasPrefix(c, "docker info")) {
			t.Errorf("no mutations without backup, ran: %v", f.calls)
			break
		}
	}
}

func TestSwarmTeardown(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker stack ls --format '{{.Name}}'", okResp("easydrop-my-cluster\nother\n"))
	f.on("docker stack rm 'easydrop-my-cluster'", okResp(""))
	f.on("rm -rf '/home/test/.easydrop/apps/my-cluster'", okResp(""))
	d := NewSwarmDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Teardown(context.Background(), "my-cluster"); err != nil {
		t.Fatalf("Teardown() unexpected error: %v", err)
	}
}

func TestSwarmTeardownIdempotent(t *testing.T) {
	f := &queueFake{}
	f.on("printf %s \"$HOME\"", okResp("/home/test\n"))
	f.on("docker info --format '{{.DockerRootDir}}", errResp())
	f.on("docker stack ls --format '{{.Name}}'", okResp("other\n"))
	f.on("rm -rf '/home/test/.easydrop/apps/my-cluster'", okResp(""))
	d := NewSwarmDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Teardown(context.Background(), "my-cluster"); err != nil {
		t.Fatalf("Teardown() must be nil, got: %v", err)
	}
	if f.ran("docker stack rm") {
		t.Errorf("missing stack must not be removed, ran: %v", f.calls)
	}
}
