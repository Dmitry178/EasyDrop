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
	// steady replays the last queued response instead of erroring when the
	// queue drains (see onSteady).
	steady bool
}

func (f *queueFake) on(prefix string, rs ...resp) {
	f.rules = append(f.rules, &qr{prefix: prefix, queue: rs})
}

// onSteady answers every call with the same response. The port pre-flight polls
// a fixed number of times, and a queue would run dry and turn the exhaustion
// into a spurious "probe failed" skip.
func (f *queueFake) onSteady(prefix string, r resp) {
	f.rules = append(f.rules, &qr{prefix: prefix, queue: []resp{r}, steady: true})
}

func (f *queueFake) ExecCommand(_ context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	for _, r := range f.rules {
		if strings.HasPrefix(cmd, r.prefix) {
			if len(r.queue) == 0 {
				if !r.steady {
					return "", "", -1, fmt.Errorf("fake: no more responses for %q", cmd)
				}
				return "", "", -1, fmt.Errorf("fake: no responses configured for %q", cmd)
			}
			res := r.queue[0]
			if len(r.queue) > 1 || !r.steady {
				r.queue = r.queue[1:]
			}
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
	// Only the pre-cleanup removal may run – the failed container stays
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

func TestDeployUsesPushedRegistryImage(t *testing.T) {
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.on("docker run -d --name 'my-api-active'", okResp("abc"))
	f.on("curl ", okResp("200"))

	app := singleAppWithPorts("my-api", 8080, 18080)
	app.Image = "ghcr.io/myorg/my-api:2.1"

	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.Deploy(context.Background(), app); err != nil {
		t.Fatalf("Deploy() unexpected error: %v", err)
	}
	if !f.ran("'ghcr.io/myorg/my-api:2.1'") {
		t.Errorf("must run the pushed registry image, ran: %v", f.calls)
	}
	if f.ran("easydrop/my-api") {
		t.Errorf("must not fall back to the remote default image, ran: %v", f.calls)
	}
}

func TestImageRefFallsBackToRemoteDefault(t *testing.T) {
	app := singleAppWithPorts("my-api", 8080, 18080)
	if got := imageRef(app); got != "easydrop/my-api:latest" {
		t.Errorf("imageRef() = %q, want remote default", got)
	}
	app.Image = "registry.example.com:5000/team/web:1.2"
	if got := imageRef(app); got != app.Image {
		t.Errorf("imageRef() = %q, want %q", got, app.Image)
	}
}

// busyListing renders an `ss -ltnpH` row for a process squatting on port. The
// pre-flight confirms a "busy" verdict up to portCheckAttempts times, so the
// fake must be able to answer every attempt.
func busyListing(port int, name string, pid int) resp {
	return okResp(fmt.Sprintf("tcp LISTEN 0 4096 0.0.0.0:%d 0.0.0.0:* users:((\"%s\",pid=%d,fd=3))",
		port, name, pid))
}

// shrinkPortCheckRetry removes the pre-flight confirmation pause so the port
// tests do not spend seconds sleeping.
func shrinkPortCheckRetry(t *testing.T) {
	t.Helper()
	orig := portCheckRetryDelay
	portCheckRetryDelay = time.Millisecond
	t.Cleanup(func() { portCheckRetryDelay = orig })
}

func TestMatchListeningPort(t *testing.T) {
	const ssListing = `tcp   LISTEN 0      4096   0.0.0.0:8080       0.0.0.0:*    users:(("nginx",pid=811,fd=6))
tcp   LISTEN 0      4096   127.0.0.1:5432    0.0.0.0:*    users:(("postgres",pid=900,fd=5))
tcp   LISTEN 0      4096   [::]:8080          [::]:*       users:(("docker-pr",pid=42,fd=3))
tcp   LISTEN 0      4096   127.0.0.1:8080     0.0.0.0:*    users:(("sshd",pid=77,fd=4))`
	for _, tc := range []struct {
		port int
		want []string
	}{
		{8080, []string{`pid=811`, `pid=42`, `pid=77`}},
		{5432, []string{`pid=900`}},
		{9999, nil},
	} {
		got := matchListeningPort(ssListing, tc.port)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("port %d: got %v, want %v", tc.port, got, tc.want)
		}
	}
	// No -p column (another user's socket): still reported as occupied, just
	// without a pid to name.
	if got := matchListeningPort("tcp LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:*", 8080); len(got) != 1 {
		t.Errorf("unattributable row must still count as occupied: %v", got)
	}
	// The netstat shape normalizes to the same `pid=N` form.
	if got := matchListeningPort("tcp 0 0 0.0.0.0:8081 0.0.0.0:* LISTEN 1234/nginx", 8081); len(got) != 1 || got[0] != "pid=1234" {
		t.Errorf("netstat PID/NAME column: got %v", got)
	}
}

func TestEnsurePortFreePassesWhenFree(t *testing.T) {
	f := &queueFake{}
	f.onSteady("ss -V", okResp(""))
	f.on("ss -ltnpH", okResp("tcp LISTEN 0 4096 127.0.0.1:5432 0.0.0.0:*"))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.ensurePortFree(context.Background(), 8080); err != nil {
		t.Errorf("free port must pass, got: %v", err)
	}
}

func TestEnsurePortFreeFailsWithActionableError(t *testing.T) {
	shrinkPortCheckRetry(t)
	f := &queueFake{}
	f.onSteady("ss -V", okResp(""))
	f.onSteady("ss -ltnpH", busyListing(8081, "other-app", 1234))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	err := d.ensurePortFree(context.Background(), 8081)
	if err == nil {
		t.Fatal("busy port must fail the pre-flight")
	}
	for _, want := range []string{"8081", "pid=1234", "app.host_port"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q, got: %v", want, err)
		}
	}
}

func TestEnsurePortFreeIgnoresToolFailure(t *testing.T) {
	shrinkPortCheckRetry(t)
	// Neither ss nor netstat exists: the deploy must proceed and let Docker be
	// the judge, rather than failing on a missing diagnostic utility.
	f := &queueFake{}
	f.on("ss -V", errResp())
	f.on("netstat -V", errResp())
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.ensurePortFree(context.Background(), 8080); err != nil {
		t.Errorf("missing listing tools must not fail the deploy, got: %v", err)
	}
}

func TestEnsurePortFreeRetriesTransientOccupancy(t *testing.T) {
	shrinkPortCheckRetry(t)
	// The container we just replaced held the port a moment ago; a single
	// busy listing is not enough to call it occupied.
	f := &queueFake{}
	f.onSteady("ss -V", okResp(""))
	f.on("ss -ltnpH", busyListing(8080, "docker-pr", 5), okResp(""), okResp(""), okResp(""))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	if err := d.ensurePortFree(context.Background(), 8080); err != nil {
		t.Errorf("a port that frees up mid-check must not fail the deploy, got: %v", err)
	}
}

func TestEnsurePortFreeFallsBackToNetstat(t *testing.T) {
	shrinkPortCheckRetry(t)
	f := &queueFake{}
	f.on("ss -V", errResp())
	f.onSteady("netstat -V", okResp(""))
	f.onSteady("netstat -ltnp", okResp(`tcp 0 0 0.0.0.0:8081 0.0.0.0:* LISTEN 1234/first-app`))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	err := d.ensurePortFree(context.Background(), 8081)
	if err == nil || !strings.Contains(err.Error(), "8081") {
		t.Errorf("netstat holder must be detected, got: %v", err)
	}
}

func TestDeployBlueGreenFailsFastOnBusyGreenPort(t *testing.T) {
	shrinkPortCheckRetry(t)
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on(inspectPrefix, okResp("8080 "))
	f.on("docker rm -f 'my-api-green'", okResp(""))
	f.onSteady("ss -V", okResp(""))
	f.onSteady("ss -ltnpH", busyListing(8081, "postgres", 900))
	d := NewSingleDriver(f)
	d.BlueGreen = true
	d.Out = &bytes.Buffer{}
	err := d.Deploy(context.Background(), singleApp("example.com"))
	if err == nil {
		t.Fatal("busy green port must fail the deploy")
	}
	if !strings.Contains(err.Error(), "8081") {
		t.Errorf("error must name the busy port, got: %v", err)
	}
	// The point of the pre-flight: nothing was staged and production stands.
	if f.ran("docker run -d --name 'my-api-green'") {
		t.Errorf("green must not be started on a busy port, ran: %v", f.calls)
	}
	if f.ran("docker stop 'my-api-active'") {
		t.Errorf("production must be untouched, ran: %v", f.calls)
	}
}

func TestDeployDirectFailsFastOnBusyHostPort(t *testing.T) {
	shrinkPortCheckRetry(t)
	f := &queueFake{}
	f.on("docker info", okResp(""))
	f.on("docker rm -f 'my-api-active'", okResp(""))
	f.onSteady("ss -V", okResp(""))
	f.onSteady("ss -ltnpH", busyListing(8080, "other", 7))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	err := d.Deploy(context.Background(), singleApp(""))
	if err == nil {
		t.Fatal("busy host port must fail the deploy")
	}
	if f.ran("docker run") {
		t.Errorf("no container may be started on a busy port, ran: %v", f.calls)
	}
}

// --- healthcheck scheme (M20) -------------------------------------------

func TestProbeSchemes(t *testing.T) {
	for _, tc := range []struct {
		configured string
		want       []string
	}{
		{"", []string{"http", "https"}},
		{"auto", []string{"http", "https"}},
		{"http", []string{"http"}},
		{"HTTP", []string{"http"}},
		{"https", []string{"https"}},
		{"  https  ", []string{"https"}},
	} {
		got := probeSchemes(tc.configured)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("probeSchemes(%q) = %v, want %v", tc.configured, got, tc.want)
		}
	}
}

func TestProbeFallsBackToHTTPS(t *testing.T) {
	// The case that used to be impossible: an app that answers only over TLS.
	// A plain-HTTP-only probe exhausted its attempts and reported a healthcheck
	// timeout for an app that was perfectly healthy.
	f := &queueFake{}
	f.on("curl -fsS ", errRespMsg("connection refused")) // http: no listener
	f.onSteady("curl -fsSk ", okResp("200"))             // https: answers
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	d.MaxProbes = 3
	d.ProbeInterval = time.Millisecond
	if err := d.probe(context.Background(), 8443, "/health", "", "staging"); err != nil {
		t.Fatalf("probe() must fall back to https, got: %v", err)
	}
	if !f.ran("curl -fsSk -o /dev/null -w '%{http_code}' 'https://localhost:8443/health'") {
		t.Errorf("the https probe must use -k, ran: %v", f.calls)
	}
	// The fallback is per attempt, not a global switch: http is still tried
	// first on later attempts so a container that gains a plain listener is
	// picked up again.
	if got := countCalls(f.calls, "curl -fsS "); got != 1 {
		t.Errorf("http must be retried on the next attempt, ran %d times", got)
	}
}

func TestProbePinnedHTTPSkipsHTTP(t *testing.T) {
	f := &queueFake{}
	f.onSteady("curl -fsSk ", okResp("200"))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	d.MaxProbes = 2
	d.ProbeInterval = time.Millisecond
	if err := d.probe(context.Background(), 8443, "/", "https", "staging"); err != nil {
		t.Fatalf("probe(): %v", err)
	}
	if f.ran("curl -fsS -o /dev/null") {
		t.Errorf("an explicit https scheme must not try http, ran: %v", f.calls)
	}
}

func TestProbePinnedHTTPSkipsHTTPOnFailure(t *testing.T) {
	f := &queueFake{}
	f.onSteady("curl -fsSk ", errRespMsg("handshake failure"))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	d.MaxProbes = 2
	d.ProbeInterval = time.Millisecond
	err := d.probe(context.Background(), 8443, "/", "https", "staging")
	if err == nil {
		t.Fatal("expected a healthcheck failure")
	}
	if f.ran("curl -fsS -o /dev/null") {
		t.Errorf("an explicit https scheme must not silently fall back, ran: %v", f.calls)
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("the error must name what was tried, got: %v", err)
	}
}

// TestProbeDoesNotFollowRedirects pins the deliberate choice not to use curl -L.
// Following every redirect would make an app pass by redirecting its health path
// at some unrelated 200 page – a false pass, which is worse than a failure.
func TestProbeDoesNotFollowRedirects(t *testing.T) {
	f := &queueFake{}
	f.onSteady("curl -fsS ", okResp("301"))
	f.onSteady("curl -fsSk ", okResp("404"))
	d := NewSingleDriver(f)
	d.Out = &bytes.Buffer{}
	d.MaxProbes = 1
	d.ProbeInterval = time.Millisecond
	if err := d.probe(context.Background(), 8080, "/health", "http", "staging"); err == nil {
		t.Fatal("a 301 must not count as healthy")
	}
	for _, c := range f.calls {
		if strings.Contains(c, "-L") || strings.Contains(c, "--location") {
			t.Errorf("the probe must not follow redirects, ran: %v", f.calls)
		}
	}
}

func TestProbeTargetIncludesPathAndPort(t *testing.T) {
	if got := probeTarget("https", 8443, "/healthz"); got != "https://localhost:8443/healthz" {
		t.Errorf("probeTarget() = %q", got)
	}
}
