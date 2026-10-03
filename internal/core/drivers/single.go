package drivers

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"easydrop/internal/core"
	"easydrop/internal/models"
)

// DeploymentDriver is the unified orchestration contract (locked in
// ARCHITECTURE.md §4). Logs covers both the MCP slice (channel drained to a
// list) and CLI tail -f streaming. Rollback restores the pre-deploy backup
// kept by Blue-Green deploys (see SingleDriver).
type DeploymentDriver interface {
	Deploy(ctx context.Context, app *models.Application) error
	Status(ctx context.Context, appName string) (*models.AppStatus, error)
	Logs(ctx context.Context, appName string, lines int, follow bool) (<-chan string, error)
	Rollback(ctx context.Context, app *models.Application) error
	Teardown(ctx context.Context, appName string) error
}

// IngressUpdater reroutes external traffic to the newly promoted container
// port. Implemented by the infra layer (M6: Nginx + Certbot). Nil means
// "skip ingress update" (e.g. local deploys without a domain).
type IngressUpdater interface {
	UpdateIngress(ctx context.Context, domain string, port int) error
}

// Probe tunables; struct fields allow tests to shrink the loop.
const (
	defaultProbeInterval = 2 * time.Second
	defaultMaxProbes     = 10
	followPollInterval   = 2 * time.Second
	followDedupWindow    = 500
	portCheckAttempts    = 3
)

// portCheckRetryDelay is the pause between "busy" confirmations. A var so tests
// can shrink it; production code never assigns it.
var portCheckRetryDelay = 500 * time.Millisecond

// SingleDriver manages isolated single-container deployments in two modes.
//
// Direct mode (BlueGreen=false, default): stop the old container (if any)
// and start the new one in place on app.host_port – simple, with brief
// downtime.
//
// Blue-Green mode (BlueGreen=true, opt-in via `driver.blue_green` or
// `deploy --blue-green`): zero-downtime swaps on the port pair
// {app.host_port, app.host_port+1} (host_port defaults to app.port). Green
// stages on whichever is free; after promotion ingress points at it, and the
// retired container is kept stopped as [app]-active-previous – the rollback
// backup consumed by Rollback. Containers always listen on app.port
// internally, so fixed-port images (nginx:80) publish fine.
//
// Progress goes to Out (os.Stderr default).
type SingleDriver struct {
	exec core.CommandExecutor
	// BlueGreen selects the deploy mode; orchestration sets it from
	// `driver.blue_green` (CLI --blue-green forces true).
	BlueGreen bool
	// Ingress is called after a probe succeeds; nil skips the update.
	Ingress IngressUpdater
	Out     io.Writer
	// ProbeInterval / MaxProbes tune the healthcheck loop (defaults above).
	// FollowInterval tunes the follow-poll loop in Logs.
	ProbeInterval  time.Duration
	MaxProbes      int
	FollowInterval time.Duration
}

// NewSingleDriver creates a driver bound to the given executor.
func NewSingleDriver(exec core.CommandExecutor) *SingleDriver {
	return &SingleDriver{exec: exec, Out: os.Stderr}
}

func (d *SingleDriver) logf(format string, args ...any) {
	fmt.Fprintf(d.Out, "easydrop: single: "+format+"\n", args...)
}

func (d *SingleDriver) probeInterval() time.Duration {
	if d.ProbeInterval > 0 {
		return d.ProbeInterval
	}
	return defaultProbeInterval
}

func (d *SingleDriver) maxProbes() int {
	if d.MaxProbes > 0 {
		return d.MaxProbes
	}
	return defaultMaxProbes
}

func (d *SingleDriver) followInterval() time.Duration {
	if d.FollowInterval > 0 {
		return d.FollowInterval
	}
	return followPollInterval
}

func activeName(appName string) string { return appName + "-active" }
func greenName(appName string) string  { return appName + "-green" }

// imageRef resolves the image a container must run: the pushed registry
// reference when set (local builds), otherwise the remote-built default.
func imageRef(app *models.Application) string {
	if app.Image != "" {
		return app.Image
	}
	return "easydrop/" + app.Config.App.Name + ":latest"
}

func q(s string) string { return core.EscapeShellArg(s) }

// backupName is the stopped pre-deploy container kept by Blue-Green deploys
// as the Rollback target. It is consumed (renamed back to active) by Rollback
// and purged by Teardown.
func backupName(appName string) string { return appName + "-active-previous" }

// Deploy validates the app and dispatches to the configured mode:
// direct in-place redeploy by default, zero-downtime Blue-Green swap when
// BlueGreen is set.
func (d *SingleDriver) Deploy(ctx context.Context, app *models.Application) error {
	if app == nil || app.Config == nil {
		return fmt.Errorf("application config is nil")
	}
	if err := models.ValidateAppName(app.Config.App.Name); err != nil {
		return err
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "docker info"); err != nil {
		return fmt.Errorf("docker daemon unreachable: %w", err)
	}
	if d.BlueGreen {
		return d.deployBlueGreen(ctx, app)
	}
	return d.deployDirect(ctx, app)
}

// deployDirect stops the old container (if any) and starts the new one in
// place, publishing app.HostPort → app.Port. Brief downtime, no backup kept:
// probe failure leaves the new container running for inspection and returns
// an error. The port pre-flight runs after the `docker rm -f`, since that
// removal is what frees the port on a redeploy.
func (d *SingleDriver) deployDirect(ctx context.Context, app *models.Application) error {
	cfg := app.Config
	name, internalPort := cfg.App.Name, cfg.App.Port
	hostPort := publishedPort(cfg)
	active := activeName(name)

	_, _, _, _ = d.exec.ExecCommand(ctx, "docker rm -f "+q(active))

	if err := d.ensurePortFree(ctx, hostPort); err != nil {
		return err
	}
	run := fmt.Sprintf("docker run -d --name %s -p %d:%d --restart unless-stopped %s",
		q(active), hostPort, internalPort, q(imageRef(app)))
	if _, _, _, err := d.exec.ExecCommand(ctx, run); err != nil {
		return fmt.Errorf("start container %s: %w", active, err)
	}
	if err := d.probe(ctx, hostPort, cfg.App.HealthCheckPath, "active"); err != nil {
		return err // container left running for inspection
	}
	if err := d.updateIngress(ctx, cfg.Nginx.Domain, hostPort); err != nil {
		return err
	}
	d.logf("%s live on host port %d (internal %d, direct)", active, hostPort, internalPort)
	return nil
}

// publishedPort resolves the host port: app.host_port when set, else app.port
// (parser defaults them equal; this keeps the driver safe for in-memory Configs).
func publishedPort(cfg *models.Config) int {
	if cfg.App.HostPort > 0 {
		return cfg.App.HostPort
	}
	return cfg.App.Port
}

// deployBlueGreen runs the zero-downtime swap: stage green on the free port
// of the {HostPort, HostPort+1} pair → probe health → update ingress → retire
// active to the stopped backup ([app]-active-previous, dropping any older
// backup) → rename green to active. Probe or ingress failure removes green and
// keeps production untouched.
func (d *SingleDriver) deployBlueGreen(ctx context.Context, app *models.Application) error {
	cfg := app.Config
	name, internalPort := cfg.App.Name, cfg.App.Port
	hostPort := publishedPort(cfg)
	active, green, backup := activeName(name), greenName(name), backupName(name)

	activePort, err := d.containerHostPort(ctx, active, internalPort)
	if err != nil {
		return err
	}
	greenPort := hostPort
	if activePort == greenPort {
		greenPort++
	}
	d.logf("staging %s on host port %d (active port: %d)...", green, greenPort, activePort)

	// Best-effort cleanup of a leftover green from a previous failed deploy.
	_, _, _, _ = d.exec.ExecCommand(ctx, "docker rm -f "+q(green))

	// Pre-flight before staging. The alternation above only knows which port
	// *this app* occupies; it cannot see a stranger on host_port+1, and
	// without this check that stranger surfaces as a bare
	// "port is already allocated" from `docker run` – after the build has
	// shipped, with no hint about which port or how to fix it.
	if err := d.ensurePortFree(ctx, greenPort); err != nil {
		return err
	}
	run := fmt.Sprintf("docker run -d --name %s -p %d:%d --restart unless-stopped %s",
		q(green), greenPort, internalPort, q(imageRef(app)))
	if _, _, _, err := d.exec.ExecCommand(ctx, run); err != nil {
		return fmt.Errorf("start staging container %s: %w", green, err)
	}

	if err := d.probe(ctx, greenPort, cfg.App.HealthCheckPath, "staging"); err != nil {
		_, _, _, _ = d.exec.ExecCommand(ctx, "docker rm -f "+q(green))
		return err
	}

	if err := d.updateIngress(ctx, cfg.Nginx.Domain, greenPort); err != nil {
		_, _, _, _ = d.exec.ExecCommand(ctx, "docker rm -f "+q(green))
		return fmt.Errorf("update ingress (production left untouched): %w", err)
	}

	if activePort != 0 {
		d.logf("retiring %s to backup %s...", active, backup)
		_, _, _, _ = d.exec.ExecCommand(ctx, "docker rm -f "+q(backup))
		if _, _, _, err := d.exec.ExecCommand(ctx, "docker stop "+q(active)); err != nil {
			return fmt.Errorf("stop retired container %s: %w", active, err)
		}
		if _, _, _, err := d.exec.ExecCommand(ctx, "docker rename "+q(active)+" "+q(backup)); err != nil {
			return fmt.Errorf("backup retired container %s: %w", active, err)
		}
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "docker rename "+q(green)+" "+q(active)); err != nil {
		return fmt.Errorf("promote %s to %s: %w", green, active, err)
	}
	d.logf("%s live on host port %d (blue-green, backup kept as %s)", active, greenPort, backup)
	return nil
}

// updateIngress reroutes traffic unless no ingress is configured.
func (d *SingleDriver) updateIngress(ctx context.Context, domain string, port int) error {
	if d.Ingress == nil || strings.TrimSpace(domain) == "" {
		d.logf("no ingress configured, skipping traffic reroute")
		return nil
	}
	d.logf("updating ingress %s -> port %d...", domain, port)
	return d.Ingress.UpdateIngress(ctx, domain, port)
}

// Rollback restores the stopped backup kept by the last Blue-Green deploy:
// remove the failed active, rename the backup back, start it, probe it.
// It consumes the backup – a second rollback reports "no backup" until the
// next Blue-Green deploy. Probe failure leaves the restored container running
// and returns an error. Without a backup (direct deploys, fresh hosts) it
// fails fast with zero host mutations beyond the existence check.
func (d *SingleDriver) Rollback(ctx context.Context, app *models.Application) error {
	if app == nil || app.Config == nil {
		return fmt.Errorf("application config is nil")
	}
	cfg := app.Config
	if err := models.ValidateAppName(cfg.App.Name); err != nil {
		return err
	}
	name := cfg.App.Name
	active, backup := activeName(name), backupName(name)

	if _, _, _, err := d.exec.ExecCommand(ctx, "docker inspect "+q(backup)); err != nil {
		return fmt.Errorf("no rollback backup for %q (only Blue-Green deploys keep %s): %w", name, backup, err)
	}
	d.logf("rolling back %s from backup %s...", active, backup)
	if _, _, _, err := d.exec.ExecCommand(ctx, "docker rm -f "+q(active)); err != nil {
		return fmt.Errorf("remove failed container %s: %w", active, err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "docker rename "+q(backup)+" "+q(active)); err != nil {
		return fmt.Errorf("restore backup %s: %w", backup, err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "docker start "+q(active)); err != nil {
		return fmt.Errorf("start restored container %s: %w", active, err)
	}
	port, err := d.containerHostPort(ctx, active, cfg.App.Port)
	if err != nil {
		return err
	}
	if port == 0 {
		port = publishedPort(cfg)
	}
	if err := d.updateIngress(ctx, cfg.Nginx.Domain, port); err != nil {
		return fmt.Errorf("update ingress after rollback: %w", err)
	}
	if err := d.probe(ctx, port, cfg.App.HealthCheckPath, "restored"); err != nil {
		return err // restored container left running – last resort stays up
	}
	d.logf("%s rolled back on host port %d", active, port)
	return nil
}

// ensurePortFree fails before a container is started when something on the
// host already listens on port.
//
// Docker's own answer ("port is already allocated") arrives only after the image
// has been built and shipped, names neither the port nor the squatter, and is
// indistinguishable from a bug in easydrop. This turns it into an actionable
// pre-flight failure instead.
//
// The check is a probe of the *listening* sockets (`ss`, falling back to
// `netstat`) rather than an attempt to bind: binding from the SSH session would
// report the foreign listener correctly but would also fail on a port held by a
// container we are about to replace, and it needs a writable temp path on a host
// that may have none. Best-effort by design – a host with neither tool, or one
// where the probe errors, proceeds to `docker run` and lets Docker be the judge,
// because a deploy must not fail on a missing diagnostic utility.
func (d *SingleDriver) ensurePortFree(ctx context.Context, port int) error {
	// Retried rather than checked once: on a redeploy the port was held by the
	// container we just removed, and docker's userland proxy can hold the
	// socket for a moment after the container is gone. Failing on that race
	// would make every redeploy flaky, so a busy result is confirmed over a
	// short window before it is believed.
	var holders []string
	for attempt := 1; attempt <= portCheckAttempts; attempt++ {
		found, err := d.portHolders(ctx, port)
		if err != nil {
			d.logf("port %d pre-flight skipped: %v", port, err)
			return nil
		}
		if len(found) == 0 {
			return nil
		}
		holders = found
		if attempt < portCheckAttempts && !sleepCtx(ctx, portCheckRetryDelay) {
			return nil // cancelled – let the deploy path report it
		}
	}
	return fmt.Errorf("host port %d is already in use by %s – Blue-Green needs the "+
		"free port of the {host_port, host_port+1} pair: free it, or set "+
		"app.host_port in easydrop.toml to another base (production untouched)",
		port, strings.Join(holders, ", "))
}

// portHolders lists the processes listening on port, best effort. An empty
// result with a nil error means "nothing found" – including the case where no
// listing tool exists, which is not a failure.
func (d *SingleDriver) portHolders(ctx context.Context, port int) ([]string, error) {
	// `ss -ltnp` is the modern listing; `netstat -ltnp` covers the older
	// images that ship without iproute2. Both print one row per socket with
	// the local address as `addr:port`.
	for _, tool := range []struct{ probe, list string }{
		{"ss -V", "ss -ltnpH"},
		{"netstat -V", "netstat -ltnp"},
	} {
		if _, _, _, err := d.exec.ExecCommand(ctx, tool.probe); err != nil {
			continue
		}
		out, _, _, err := d.exec.ExecCommand(ctx, tool.list)
		if err != nil {
			return nil, err
		}
		return matchListeningPort(out, port), nil
	}
	return nil, nil // no listing tool – not an error, see ensurePortFree
}

// matchListeningPort picks the rows whose local address ends in :port. It
// matches on the address suffix rather than a field index, because the field
// layout differs between ss and netstat and neither is worth parsing fully –
// only the local address matters here.
func matchListeningPort(listing string, port int) []string {
	suffix := ":" + strconv.Itoa(port)
	var holders []string
	seen := map[string]bool{}
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if !slices.ContainsFunc(fields, func(f string) bool { return strings.HasSuffix(f, suffix) }) {
			continue
		}
		// The process column, when the caller may see it: ss prints
		// users:(("nginx",pid=811,fd=6)), netstat prints 1234/nginx. Both carry
		// `pid=`; without it the row is still known to be occupied, just not
		// attributable, so it is reported as busy with no holder named.
		holder := pidField(fields)
		if holder == "" {
			// Occupied but unattributable: the socket belongs to another user
			// (a root-owned docker-proxy when easydrop runs unprivileged is the
			// common case), so `-p` cannot name it. Saying "occupied" is the
			// actionable part; inventing an owner would not be.
			holder = "a process whose owner this user cannot see"
		}
		if seen[holder] {
			continue
		}
		seen[holder] = true
		holders = append(holders, holder)
	}
	return holders
}

// pidField extracts an owner identifier from a socket-listing row. The two
// tools format it differently – ss prints `users:(("nginx",pid=811,fd=6))`,
// netstat `1234/nginx` – so both shapes are recognized and normalized to
// `pid=N`. Returns "" when the row carries no owner, which happens whenever the
// socket belongs to another user: the port is still occupied, just not ours to
// name.
func pidField(fields []string) string {
	for _, f := range fields {
		if open := strings.Index(f, "pid="); open >= 0 {
			rest := f[open+len("pid="):]
			if end := strings.IndexAny(rest, ",)"); end >= 0 {
				rest = rest[:end]
			}
			if rest != "" {
				return "pid=" + rest
			}
		}
	}
	// netstat: a `PID/NAME` column, e.g. `1234/nginx`.
	for _, f := range fields {
		num, name, ok := strings.Cut(f, "/")
		if !ok || name == "" {
			continue
		}
		if _, err := strconv.Atoi(num); err == nil {
			return "pid=" + num
		}
	}
	return ""
}

// containerHostPort returns the published host port of containerName for the
// given internal port, or 0 when the container does not exist.
func (d *SingleDriver) containerHostPort(ctx context.Context, containerName string, internalPort int) (int, error) {
	out, _, _, err := d.exec.ExecCommand(ctx,
		"docker inspect -f '{{range $p, $c := .NetworkSettings.Ports}}{{if $c}}{{(index $c 0).HostPort}} {{end}}{{end}}' "+q(containerName))
	if err != nil {
		return 0, nil // container does not exist – first deploy
	}
	for _, field := range strings.Fields(out) {
		if port, perr := strconv.Atoi(field); perr == nil {
			return port, nil
		}
	}
	return 0, fmt.Errorf("container %s exists but publishes no host port", containerName)
}

// probe polls http://localhost:[port][path] until a 200 arrives or the
// attempts run out. Any error or non-200 is a miss, not a failure. role
// labels the container under test in messages ("staging", "active",
// "restored").
func (d *SingleDriver) probe(ctx context.Context, port int, healthPath, role string) error {
	if healthPath == "" {
		healthPath = "/"
	}
	target := fmt.Sprintf("http://localhost:%d%s", port, healthPath)
	max := d.maxProbes()
	for attempt := 1; attempt <= max; attempt++ {
		if ctx.Err() != nil {
			return fmt.Errorf("deploy cancelled while probing %s: %w", target, ctx.Err())
		}
		out, _, _, err := d.exec.ExecCommand(ctx,
			"curl -fsS -o /dev/null -w '%{http_code}' "+q(target))
		if err == nil && strings.TrimSpace(out) == "200" {
			d.logf("healthcheck %s OK (attempt %d/%d)", target, attempt, max)
			return nil
		}
		d.logf("healthcheck %s miss (attempt %d/%d), retrying...", target, attempt, max)
		if attempt < max && !sleepCtx(ctx, d.probeInterval()) {
			return fmt.Errorf("deploy cancelled while probing %s: %w", target, ctx.Err())
		}
	}
	return fmt.Errorf("%s failed healthcheck %s after %d attempts (production left untouched)", role, target, max)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Status inspects the active container: running → Up, restarting →
// Restarting, anything else (or missing) → Down. Uptime comes from
// `docker ps` RunningFor. SSLStatus is left empty – the ingress layer (M6)
// enriches it once certificates are managed.
func (d *SingleDriver) Status(ctx context.Context, appName string) (*models.AppStatus, error) {
	if err := models.ValidateAppName(appName); err != nil {
		return nil, err
	}
	out, _, _, err := d.exec.ExecCommand(ctx,
		"docker ps -a --filter "+q("name=^/"+activeName(appName)+"$")+` --format "{{.State}}|{{.RunningFor}}"`)
	if err != nil {
		return nil, fmt.Errorf("query container status: %w", err)
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return &models.AppStatus{Status: "Down"}, nil
	}
	parts := strings.SplitN(line, "|", 2)
	state := strings.ToLower(strings.TrimSpace(parts[0]))
	uptime := ""
	if len(parts) == 2 {
		uptime = strings.TrimSpace(parts[1])
	}
	switch state {
	case "running":
		return &models.AppStatus{Status: "Up", Uptime: uptime}, nil
	case "restarting":
		return &models.AppStatus{Status: "Restarting", Uptime: uptime}, nil
	default:
		return &models.AppStatus{Status: "Down"}, nil
	}
}

// Logs streams `docker logs` output. With follow=false it emits --tail lines
// and closes the channel. With follow=true it emits the tail snapshot, then
// polls every 2s emitting only unseen lines (dedup window of the last 500)
// until ctx is cancelled – polling stays ctx-aware on both local and SSH
// executors, where a blocking `docker logs -f` session could not be stopped.
func (d *SingleDriver) Logs(ctx context.Context, appName string, lines int, follow bool) (<-chan string, error) {
	if err := models.ValidateAppName(appName); err != nil {
		return nil, err
	}
	if lines < 0 {
		lines = 0
	}
	tail := fmt.Sprintf("docker logs --tail %d %s", lines, q(activeName(appName)))
	ch := make(chan string, 100)
	go streamLines(ctx, func(ctx context.Context) (string, error) {
		out, _, _, err := d.exec.ExecCommand(ctx, tail)
		return out, err
	}, follow, d.followInterval(), ch)
	return ch, nil
}

// Teardown force-removes active, any leftover green, and the rollback backup.
// Missing containers are not an error – teardown is idempotent.
func (d *SingleDriver) Teardown(ctx context.Context, appName string) error {
	if err := models.ValidateAppName(appName); err != nil {
		return err
	}
	for _, c := range []string{activeName(appName), greenName(appName), backupName(appName)} {
		if _, _, _, err := d.exec.ExecCommand(ctx, "docker inspect "+q(c)); err != nil {
			d.logf("%s not present, skipping", c)
			continue
		}
		d.logf("removing %s...", c)
		if _, _, _, err := d.exec.ExecCommand(ctx, "docker rm -f "+q(c)); err != nil {
			return fmt.Errorf("remove container %s: %w", c, err)
		}
	}
	return nil
}
