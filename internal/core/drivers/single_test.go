package drivers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"easydrop/internal/models"
)

type resp struct {
	stdout string
	err    error
}

func okResp(s string) resp     { return resp{stdout: s} }
func errResp() resp            { return resp{err: fmt.Errorf("fake: exit 1")} }
func errRespMsg(m string) resp { return resp{err: fmt.Errorf("fake: %s", m)} }

// queueFake serves queued responses matched by command prefix and records calls.
type queueFake struct {
	rules []*qr
	calls []string
}

type qr struct {
	prefix string
	queue  []resp
}

func (f *queueFake) on(prefix string, rs ...resp) {
	f.rules = append(f.rules, &qr{prefix: prefix, queue: rs})
}

func (f *queueFake) ExecCommand(_ context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	for _, r := range f.rules {
		if strings.HasPrefix(cmd, r.prefix) {
			if len(r.queue) == 0 {
				return "", "", -1, fmt.Errorf("fake: no more responses for %q", cmd)
			}
			res := r.queue[0]
			r.queue = r.queue[1:]
			if res.err != nil {
				return res.stdout, "", 1, res.err
			}
			return res.stdout, "", 0, nil
		}
	}
	return "", "", -1, fmt.Errorf("fake: unexpected command %q", cmd)
}

func (f *queueFake) UploadFile(_ context.Context, _, _ string) error { return nil }
func (f *queueFake) Close() error                                    { return nil }

func (f *queueFake) ran(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

const inspectPrefix = "docker inspect -f"

func singleApp(domain string) *models.Application {
	return &models.Application{Config: &models.Config{
		App:   models.AppConfig{Name: "my-api", Port: 8080, HealthCheckPath: "/health"},
		Nginx: models.NginxConfig{Domain: domain},
	}}
}

// stubIngress records UpdateIngress calls.
type stubIngress struct {
	domain string
	port   int
	calls  int
	err    error
}

func (s *stubIngress) UpdateIngress(_ context.Context, domain string, port int) error {
	s.domain, s.port, s.calls = domain, port, s.calls+1
	return s.err
}

func TestDeployFirstTime(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on(inspectPrefix, errResp()) // no active yet
	f.on("docker rm -f 'my-api-green'", okResp(""))
	f.on("docker run -d --name 'my-api-green'", okResp("abc123"))
	f.on("curl ", okResp("200"))
	f.on("docker rename", okResp(""))

	d := NewSingleDriver(f)
	d.BlueGreen = true
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), singleApp("")); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("-p 8080:8080") {
		t.Errorf("first deploy must stage green on app port 8080, ran: %v", f.calls)
	}
	if f.ran("docker stop") {
		t.Errorf("nothing to retire on first deploy, ran: %v", f.calls)
	}
}

func TestDeploySecondTimeAlternatesPort(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on(inspectPrefix, okResp("8080 ")) // active serves 8080
	f.on("docker rm -f 'my-api-green'", okResp(""))
	f.on("docker run -d --name 'my-api-green'", okResp("abc123"))
	f.on("curl ", okResp("200"))
	f.on("docker rm -f 'my-api-active-previous'", okResp(""))
	f.on("docker stop 'my-api-active'", okResp(""))
	f.on("docker rename 'my-api-active' 'my-api-active-previous'", okResp(""))
	f.on("docker rename", okResp(""))

	ing := &stubIngress{}
	d := NewSingleDriver(f)
	d.BlueGreen = true
	d.Ingress = ing
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), singleApp("example.com")); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("-p 8081:8080") {
		t.Errorf("second deploy must stage green on 8081, ran: %v", f.calls)
	}
	if ing.calls != 1 || ing.domain != "example.com" || ing.port != 8081 {
		t.Errorf("ingress must be updated to example.com:8081, got %+v", ing)
	}
	if !f.ran("docker rename 'my-api-green' 'my-api-active'") {
		t.Errorf("green must be promoted, ran: %v", f.calls)
	}
	if !f.ran("docker rename 'my-api-active' 'my-api-active-previous'") {
		t.Errorf("retired active must be kept as backup, ran: %v", f.calls)
	}
	if f.ran("docker rm 'my-api-active'") {
		t.Errorf("retired active must NOT be removed in blue-green mode, ran: %v", f.calls)
	}
}

func TestDeployProbeFailureKeepsProduction(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on(inspectPrefix, okResp("8080 "))
	f.on("docker rm -f 'my-api-green'", okResp(""), okResp(""))
	f.on("docker run -d --name 'my-api-green'", okResp("abc123"))
	f.on("curl ", errRespMsg("500"), errRespMsg("500"), errRespMsg("500"))

	d := NewSingleDriver(f)
	d.BlueGreen = true
	d.Out = &bytes.Buffer{}
	d.MaxProbes = 3
	d.ProbeInterval = time.Millisecond
	err := d.Deploy(context.Background(), singleApp("example.com"))
	if err == nil {
		t.Fatalf("Deploy() expected healthcheck error, got nil")
	}
	if !strings.Contains(err.Error(), "healthcheck") {
		t.Errorf("error should mention healthcheck, got: %v", err)
	}
	if f.ran("docker stop 'my-api-active'") || f.ran("docker rename") {
		t.Errorf("production must stay untouched on probe failure, ran: %v", f.calls)
	}
	if got := countCalls(f.calls, "docker rm -f 'my-api-green'"); got != 2 {
		t.Errorf("green cleanup must run (pre + rollback), ran %d times", got)
	}
}

func TestDeployIngressFailureRollsBack(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on(inspectPrefix, okResp("8080 "))
	f.on("docker rm -f 'my-api-green'", okResp(""), okResp(""))
	f.on("docker run -d --name 'my-api-green'", okResp("abc123"))
	f.on("curl ", okResp("200"))

	ing := &stubIngress{err: fmt.Errorf("nginx reload failed")}
	d := NewSingleDriver(f)
	d.BlueGreen = true
	d.Ingress = ing
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), singleApp("example.com")); err == nil {
		t.Fatalf("Deploy() expected ingress error, got nil")
	}
	if f.ran("docker stop 'my-api-active'") || f.ran("docker rename") {
		t.Errorf("production must stay untouched on ingress failure, ran: %v", f.calls)
	}
}

func countCalls(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestStatusTable(t *testing.T) {
	cases := []struct {
		name       string
		psOut      string
		wantStatus string
		wantUptime string
	}{
		{"running", "running|5 minutes", "Up", "5 minutes"},
		{"restarting", "restarting|10 seconds", "Restarting", "10 seconds"},
		{"exited", "exited|", "Down", ""},
		{"missing", "", "Down", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &queueFake{}
			f.on("docker ps", okResp(tc.psOut))
			d := NewSingleDriver(f)
			st, err := d.Status(context.Background(), "my-api")
			if err != nil {
				t.Fatalf("Status() unexpected error: %v", err)
			}
			if st.Status != tc.wantStatus || st.Uptime != tc.wantUptime {
				t.Errorf("Status() = %+v, want {%s %s}", st, tc.wantStatus, tc.wantUptime)
			}
		})
	}
}

func TestStatusInvalidName(t *testing.T) {
	d := NewSingleDriver(&queueFake{})
	if _, err := d.Status(context.Background(), "BAD NAME"); err == nil {
		t.Errorf("Status() expected validation error, got nil")
	}
}

func collectLogs(ch <-chan string) []string {
	var out []string
	for l := range ch {
		out = append(out, l)
	}
	return out
}

func TestLogsSnapshot(t *testing.T) {
	f := &queueFake{}
	f.on("docker logs", okResp("line1\nline2\nline3\n"))
	d := NewSingleDriver(f)
	ch, err := d.Logs(context.Background(), "my-api", 100, false)
	if err != nil {
		t.Fatalf("Logs() unexpected error: %v", err)
	}
	got := collectLogs(ch)
	if len(got) != 3 || got[0] != "line1" || got[2] != "line3" {
		t.Errorf("Logs() = %v, want 3 lines", got)
	}
}

func TestLogsFollowStreamsAndStops(t *testing.T) {
	f := &queueFake{}
	f.on("docker logs", okResp("a\nb\n"), okResp("a\nb\nc\n"), okResp("a\nb\nc\n"))
	d := NewSingleDriver(f)
	d.FollowInterval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ch, err := d.Logs(ctx, "my-api", 100, true)
	if err != nil {
		t.Fatalf("Logs() unexpected error: %v", err)
	}
	got := collectLogs(ch)
	joined := strings.Join(got, ",")
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(joined, want) {
			t.Errorf("follow stream missing %q, got %v", want, got)
		}
	}
	if count := strings.Count(joined, "a"); count != 1 {
		t.Errorf("dedup broken: 'a' emitted %d times in %v", count, got)
	}
}

func TestTeardown(t *testing.T) {
	f := &queueFake{}
	f.on("docker inspect 'my-api-active'", okResp("{}"))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker inspect 'my-api-green'", errResp()) // no leftover
	f.on("docker inspect 'my-api-active-previous'", okResp("{}"))
	f.on("docker rm -f 'my-api-active-previous'", okResp(""))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Teardown(context.Background(), "my-api"); err != nil {
		t.Fatalf("Teardown() unexpected error: %v", err)
	}
	if !f.ran("docker rm -f 'my-api-active'") {
		t.Errorf("active must be removed, ran: %v", f.calls)
	}
	if !f.ran("docker rm -f 'my-api-active-previous'") {
		t.Errorf("backup must be removed, ran: %v", f.calls)
	}
}

func TestTeardownIdempotent(t *testing.T) {
	f := &queueFake{}
	f.on("docker inspect", errResp(), errResp(), errResp())
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Teardown(context.Background(), "my-api"); err != nil {
		t.Fatalf("Teardown() on empty host must be nil, got: %v", err)
	}
	if f.ran("docker rm") {
		t.Errorf("nothing to remove, ran: %v", f.calls)
	}
}

func TestDeployDirectFirstTime(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker run -d --name 'my-api-active'", okResp("abc123"))
	f.on("curl ", okResp("200"))

	d := NewSingleDriver(f) // BlueGreen defaults to false
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), singleApp("")); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("-p 8080:8080") {
		t.Errorf("direct deploy must run active on 8080:8080, ran: %v", f.calls)
	}
	if f.ran("green") {
		t.Errorf("direct deploy must not touch green containers, ran: %v", f.calls)
	}
	if f.ran("docker rename") || f.ran("docker inspect") {
		t.Errorf("direct deploy needs no inspect/rename, ran: %v", f.calls)
	}
}

func TestDeployDirectReplacesActive(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker run -d --name 'my-api-active'", okResp("abc123"))
	f.on("curl ", okResp("200"))

	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), singleApp("")); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if got := countCalls(f.calls, "docker rm -f 'my-api-active'"); got != 1 {
		t.Errorf("old active must be force-removed once, ran %d times", got)
	}
}

func TestDeployDirectProbeFailureKeepsContainer(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker run -d --name 'my-api-active'", okResp("abc123"))
	f.on("curl ", errRespMsg("500"), errRespMsg("500"))

	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	d.MaxProbes = 2
	d.ProbeInterval = time.Millisecond
	if err := d.Deploy(context.Background(), singleApp("")); err == nil {
		t.Fatalf("Deploy() expected healthcheck error, got nil")
	}
	// Only the pre-cleanup removal may run — the failed container stays
	// for inspection.
	if got := countCalls(f.calls, "docker rm -f 'my-api-active'"); got != 1 {
		t.Errorf("failed direct container must be kept, rm ran %d times", got)
	}
}

func TestRollbackHappyPath(t *testing.T) {
	f := &queueFake{}
	f.on("docker inspect 'my-api-active-previous'", okResp("{}"))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker rename 'my-api-active-previous' 'my-api-active'", okResp(""))
	f.on("docker start 'my-api-active'", okResp(""))
	f.on(inspectPrefix, okResp("8081 "))
	f.on("curl ", okResp("200"))

	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Rollback(context.Background(), singleApp("")); err != nil {
		t.Fatalf("Rollback() unexpected error: %v", err)
	}
	if !f.ran("docker start 'my-api-active'") {
		t.Errorf("restored container must be started, ran: %v", f.calls)
	}
}

func TestRollbackNoBackup(t *testing.T) {
	f := &queueFake{}
	f.on("docker inspect 'my-api-active-previous'", errResp())

	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	err := d.Rollback(context.Background(), singleApp(""))
	if err == nil {
		t.Fatalf("Rollback() expected no-backup error, got nil")
	}
	if !strings.Contains(err.Error(), "no rollback backup") {
		t.Errorf("error should explain the missing backup, got: %v", err)
	}
	if len(f.calls) != 1 {
		t.Errorf("no mutations allowed without backup, ran: %v", f.calls)
	}
}

func TestRollbackProbeFailureKeepsRestored(t *testing.T) {
	f := &queueFake{}
	f.on("docker inspect 'my-api-active-previous'", okResp("{}"))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker rename 'my-api-active-previous' 'my-api-active'", okResp(""))
	f.on("docker start 'my-api-active'", okResp(""))
	f.on(inspectPrefix, okResp("8081 "))
	f.on("curl ", errRespMsg("500"), errRespMsg("500"))

	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	d.MaxProbes = 2
	d.ProbeInterval = time.Millisecond
	if err := d.Rollback(context.Background(), singleApp("")); err == nil {
		t.Fatalf("Rollback() expected healthcheck error, got nil")
	}
	// Failed active was removed once; restored container stays up.
	if got := countCalls(f.calls, "docker rm -f 'my-api-active'"); got != 1 {
		t.Errorf("restored container must be kept, rm ran %d times", got)
	}
}

func TestRollbackInvalidName(t *testing.T) {
	f := &queueFake{}
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	bad := &models.Application{Config: &models.Config{
		App: models.AppConfig{Name: "BAD NAME", Port: 8080},
	}}
	if err := d.Rollback(context.Background(), bad); err == nil {
		t.Errorf("Rollback() expected validation error, got nil")
	}
	if len(f.calls) != 0 {
		t.Errorf("no host commands must run for invalid name, ran: %v", f.calls)
	}
}

func singleAppWithPorts(name string, port, hostPort int) *models.Application {
	return &models.Application{Config: &models.Config{
		App:   models.AppConfig{Name: name, Port: port, HostPort: hostPort, HealthCheckPath: "/health"},
		Nginx: models.NginxConfig{Domain: ""},
	}}
}

func TestDeployDirectPublishesHostPort(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker run -d --name 'my-api-active'", okResp("abc"))
	f.on("curl ", okResp("200"))

	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), singleAppWithPorts("my-api", 80, 18080)); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("-p 18080:80") {
		t.Errorf("must publish host 18080 -> internal 80, ran: %v", f.calls)
	}
	if f.ran("localhost:80") {
		t.Errorf("probe must hit the host port, not the internal one, ran: %v", f.calls)
	}
}

func TestDeployBlueGreenPairUsesHostPort(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on(inspectPrefix, okResp("18080 "))
	f.on("docker rm -f 'my-api-green'", okResp(""))
	f.on("docker run -d --name 'my-api-green'", okResp("abc"))
	f.on("curl ", okResp("200"))
	f.on("docker rm -f 'my-api-active-previous'", okResp(""))
	f.on("docker stop 'my-api-active'", okResp(""))
	f.on("docker rename 'my-api-active' 'my-api-active-previous'", okResp(""))
	f.on("docker rename", okResp(""))

	d := NewSingleDriver(f)
	d.BlueGreen = true
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), singleAppWithPorts("my-api", 80, 18080)); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("-p 18081:80") {
		t.Errorf("blue-green must alternate on {18080, 18081} keeping internal 80, ran: %v", f.calls)
	}
}

func TestPublishedPortFallsBackToPort(t *testing.T) {
	cfg := &models.Config{App: models.AppConfig{Name: "a", Port: 8080}}
	if got := publishedPort(cfg); got != 8080 {
		t.Errorf("publishedPort() = %d, want 8080 when host_port unset", got)
	}
	cfg.App.HostPort = 18080
	if got := publishedPort(cfg); got != 18080 {
		t.Errorf("publishedPort() = %d, want 18080", got)
	}
}
