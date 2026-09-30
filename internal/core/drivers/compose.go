package drivers

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"easydrop/internal/core"
	"easydrop/internal/core/builder"
	"easydrop/internal/models"
)

// appsDir is the persistent per-app directory on the target host holding the
// live compose file and its rollback backup. The home directory is resolved
// once via the executor (never single-quote $HOME: neither POSIX shells nor
// snap-confined binaries expand it there — see OD-02).
func resolveHome(ctx context.Context, ex core.CommandExecutor) (string, error) {
	out, _, _, err := ex.ExecCommand(ctx, `printf %s "$HOME"`)
	if err != nil {
		return "", fmt.Errorf("resolve home on target host: %w", err)
	}
	home := strings.TrimSpace(out)
	if home == "" || strings.HasPrefix(home, "/var/lib/snapd/void") {
		return "", fmt.Errorf("unusable $HOME on target host (%q): set HOME for the SSH user", home)
	}
	return home, nil
}

func projectName(appName string) string { return "easydrop-" + appName }

// streamLines emits fetch() output line by line: once for snapshots,
// polling for follow. Unseen-line dedup (window of followDedupWindow) keeps
// polls idempotent; ctx cancellation stops the loop. Polling (not blocking
// -f) stays ctx-aware on both Local and SSH executors.
func streamLines(ctx context.Context, fetch func(context.Context) (string, error), follow bool, interval time.Duration, ch chan<- string) {
	defer close(ch)
	seen := map[string]bool{}
	var recent []string
	emit := func(out string) {
		for _, line := range strings.Split(out, "\n") {
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			recent = append(recent, line)
			if len(recent) > followDedupWindow {
				delete(seen, recent[0])
				recent = recent[1:]
			}
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if out, err := fetch(ctx); err == nil {
			emit(out)
		} else if !follow {
			return
		}
		if !follow {
			return
		}
		if !sleepCtx(ctx, interval) {
			return
		}
	}
}

// ComposeDriver deploys multi-container stacks from a compose file (FR-06).
// The workspace is archived and shipped like RemoteBuilder (so `build:`
// contexts resolve on the host), then `up -d --build` runs there. The live
// compose file persists at ~/.easydrop/apps/[app]/compose.yml with its
// rollback backup beside it. Progress goes to Out (os.Stderr default).
type ComposeDriver struct {
	exec core.CommandExecutor
	// SrcDir is the workspace to archive; empty means the process cwd.
	SrcDir string
	// Ingress is called after a successful up; nil skips the update.
	Ingress IngressUpdater
	Out     io.Writer
	// FollowInterval tunes the follow-poll loop in Logs (default 2s).
	FollowInterval time.Duration
	// homeDir caches resolveHome per driver instance.
	homeDir string
}

// NewComposeDriver creates a driver bound to the given executor.
func NewComposeDriver(exec core.CommandExecutor) *ComposeDriver {
	return &ComposeDriver{exec: exec, Out: os.Stderr}
}

func (d *ComposeDriver) logf(format string, args ...any) {
	fmt.Fprintf(d.Out, "easydrop: compose: "+format+"\n", args...)
}

func (d *ComposeDriver) followInterval() time.Duration {
	if d.FollowInterval > 0 {
		return d.FollowInterval
	}
	return followPollInterval
}

// appDir resolves the persistent per-app dir, caching the remote home.
// Snap-confined daemons cannot read hidden files in $HOME (snapd's home
// interface), so the state dir drops the leading dot there.
func (d *ComposeDriver) appDir(ctx context.Context, appName string) (string, error) {
	if d.homeDir == "" {
		home, err := resolveHome(ctx, d.exec)
		if err != nil {
			return "", err
		}
		d.homeDir = home
	}
	return stateDir(d.exec, ctx, d.homeDir) + "/" + appName, nil
}

// stateDir returns the per-host state root for compose/swarm app files.
func stateDir(ex core.CommandExecutor, ctx context.Context, home string) string {
	if builder.IsSnapConfinedDaemon(ctx, ex) {
		return home + "/easydrop/apps"
	}
	return home + "/.easydrop/apps"
}

func (d *ComposeDriver) srcDir() (string, error) {
	if d.SrcDir != "" {
		return d.SrcDir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve workspace dir: %w", err)
	}
	return cwd, nil
}

// Deploy ships the workspace, runs `up -d --build`, verifies every service
// is running, persists the live compose file (+ backup of the replaced one)
// and purges staging. `up` failure keeps staging for debugging.
func (d *ComposeDriver) Deploy(ctx context.Context, app *models.Application) error {
	if app == nil || app.Config == nil {
		return fmt.Errorf("application config is nil")
	}
	cfg := app.Config
	if err := models.ValidateAppName(cfg.App.Name); err != nil {
		return err
	}
	name, project := cfg.App.Name, projectName(cfg.App.Name)
	composeFile := cfg.Driver.ComposeFile
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}
	srcDir, err := d.srcDir()
	if err != nil {
		return err
	}

	archive, err := builder.ArchiveWorkspace(srcDir)
	if err != nil {
		return err
	}
	defer os.Remove(archive)

	remoteDir := core.StagingBase() + "/builds/" + name
	if err := builder.StageWorkspace(ctx, d.exec, archive, remoteDir); err != nil {
		return err
	}
	remoteCompose := remoteDir + "/" + composeFile
	base := fmt.Sprintf("docker compose -p %s -f %s", q(project), q(remoteCompose))

	d.logf("bringing up stack %s...", project)
	if _, _, _, err := d.exec.ExecCommand(ctx, base+" up -d --build --remove-orphans"); err != nil {
		return fmt.Errorf("compose up (staging kept at %s for debugging): %w", remoteDir, err)
	}
	if err := d.checkRunning(ctx, base); err != nil {
		return err
	}

	dir, err := d.appDir(ctx, name)
	if err != nil {
		return err
	}
	live, prev := dir+"/compose.yml", dir+"/compose.previous.yml"
	if _, _, _, err := d.exec.ExecCommand(ctx, "mkdir -p "+q(dir)); err != nil {
		return fmt.Errorf("create app dir %s: %w", dir, err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "test -f "+q(live)); err == nil {
		if _, _, _, err := d.exec.ExecCommand(ctx, fmt.Sprintf("cp -f %s %s", q(live), q(prev))); err != nil {
			return fmt.Errorf("backup live compose file: %w", err)
		}
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, fmt.Sprintf("cp -f %s %s", q(remoteCompose), q(live))); err != nil {
		return fmt.Errorf("persist live compose file: %w", err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, fmt.Sprintf("test -f %s && cp -f %s %s || true",
		q(remoteDir+"/.env"), q(remoteDir+"/.env"), q(dir+"/.env"))); err != nil {
		return fmt.Errorf("persist .env: %w", err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "rm -rf "+q(remoteDir)); err != nil {
		return fmt.Errorf("purge staging dir %s: %w", remoteDir, err)
	}

	if d.Ingress != nil && strings.TrimSpace(cfg.Nginx.Domain) != "" {
		if err := d.Ingress.UpdateIngress(ctx, cfg.Nginx.Domain, cfg.App.Port); err != nil {
			return fmt.Errorf("update ingress: %w", err)
		}
	} else {
		d.logf("no ingress configured, skipping traffic reroute")
	}
	d.logf("stack %s live", project)
	return nil
}

// checkRunning requires every stack service to be in running state.
func (d *ComposeDriver) checkRunning(ctx context.Context, base string) error {
	out, _, _, err := d.exec.ExecCommand(ctx, base+` ps --format '{{.Service}}|{{.State}}'`)
	if err != nil {
		return fmt.Errorf("query stack services: %w", err)
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return fmt.Errorf("compose stack reports no services")
	}
	var bad []string
	for _, l := range lines {
		parts := strings.SplitN(l, "|", 2)
		if len(parts) != 2 || parts[1] != "running" {
			bad = append(bad, l)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("stack services not running: %s", strings.Join(bad, ", "))
	}
	return nil
}

func nonEmptyLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// liveCompose resolves the persistent compose file, erroring when the app
// was never deployed.
func (d *ComposeDriver) liveCompose(ctx context.Context, appName string) (project, file string, err error) {
	dir, err := d.appDir(ctx, appName)
	if err != nil {
		return "", "", err
	}
	file = dir + "/compose.yml"
	if _, _, _, err := d.exec.ExecCommand(ctx, "test -f "+q(file)); err != nil {
		return "", "", fmt.Errorf("compose deployment %q not found on host", appName)
	}
	return projectName(appName), file, nil
}

// Status aggregates service states: all running → Up, any restarting →
// Restarting, otherwise Down. Uptime mirrors the first service's Status.
func (d *ComposeDriver) Status(ctx context.Context, appName string) (*models.AppStatus, error) {
	if err := models.ValidateAppName(appName); err != nil {
		return nil, err
	}
	project, file, err := d.liveCompose(ctx, appName)
	if err != nil {
		return &models.AppStatus{Status: "Down"}, nil
	}
	out, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker compose -p %s -f %s", q(project), q(file))+` ps --format '{{.Service}}|{{.State}}|{{.Status}}'`)
	if err != nil {
		return nil, fmt.Errorf("query stack services: %w", err)
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return &models.AppStatus{Status: "Down"}, nil
	}
	uptime := ""
	restarting := false
	for _, l := range lines {
		parts := strings.SplitN(l, "|", 3)
		state := ""
		if len(parts) >= 2 {
			state = strings.ToLower(strings.TrimSpace(parts[1]))
		}
		if len(parts) == 3 && uptime == "" {
			uptime = strings.TrimSpace(parts[2])
		}
		switch state {
		case "running":
		case "restarting":
			restarting = true
		default:
			return &models.AppStatus{Status: "Down"}, nil
		}
	}
	if restarting {
		return &models.AppStatus{Status: "Restarting", Uptime: uptime}, nil
	}
	return &models.AppStatus{Status: "Up", Uptime: uptime}, nil
}

// Logs streams `compose logs` across all services (with service prefixes).
func (d *ComposeDriver) Logs(ctx context.Context, appName string, lines int, follow bool) (<-chan string, error) {
	if err := models.ValidateAppName(appName); err != nil {
		return nil, err
	}
	if lines < 0 {
		lines = 0
	}
	project, file, err := d.liveCompose(ctx, appName)
	if err != nil {
		return nil, err
	}
	tail := fmt.Sprintf("docker compose -p %s -f %s logs --tail %d", q(project), q(file), lines)
	ch := make(chan string, 100)
	go streamLines(ctx, func(ctx context.Context) (string, error) {
		out, _, _, err := d.exec.ExecCommand(ctx, tail)
		return out, err
	}, follow, d.followInterval(), ch)
	return ch, nil
}

// Rollback restores the previous compose file and re-runs `up -d`
// (images are cached — no rebuild). It consumes the backup: a second
// rollback reports "no backup" until the next replacing deploy.
func (d *ComposeDriver) Rollback(ctx context.Context, app *models.Application) error {
	if app == nil || app.Config == nil {
		return fmt.Errorf("application config is nil")
	}
	cfg := app.Config
	if err := models.ValidateAppName(cfg.App.Name); err != nil {
		return err
	}
	name, project := cfg.App.Name, projectName(cfg.App.Name)
	dir, err := d.appDir(ctx, name)
	if err != nil {
		return err
	}
	live, prev := dir+"/compose.yml", dir+"/compose.previous.yml"

	if _, _, _, err := d.exec.ExecCommand(ctx, "test -f "+q(prev)); err != nil {
		return fmt.Errorf("no rollback backup for %q (only compose deploys that replaced a live stack keep one)", name)
	}
	d.logf("rolling back stack %s...", project)
	if _, _, _, err := d.exec.ExecCommand(ctx, fmt.Sprintf("cp -f %s %s", q(prev), q(live))); err != nil {
		return fmt.Errorf("restore previous compose file: %w", err)
	}
	base := fmt.Sprintf("docker compose -p %s -f %s", q(project), q(live))
	if _, _, _, err := d.exec.ExecCommand(ctx, base+" up -d --remove-orphans"); err != nil {
		return fmt.Errorf("compose up after rollback: %w", err)
	}
	if err := d.checkRunning(ctx, base); err != nil {
		return fmt.Errorf("rollback health: %w", err)
	}
	_, _, _, _ = d.exec.ExecCommand(ctx, "rm -f "+q(prev))
	d.logf("stack %s rolled back", project)
	return nil
}

// Teardown runs `compose down` (volumes are kept — data is never deleted)
// and removes the persisted app dir. Missing deployments are not an error.
func (d *ComposeDriver) Teardown(ctx context.Context, appName string) error {
	if err := models.ValidateAppName(appName); err != nil {
		return err
	}
	dir, err := d.appDir(ctx, appName)
	if err != nil {
		return err
	}
	file := dir + "/compose.yml"
	if _, _, _, err := d.exec.ExecCommand(ctx, "test -f "+q(file)); err != nil {
		d.logf("%s not deployed, skipping", appName)
		_, _, _, _ = d.exec.ExecCommand(ctx, "rm -rf "+q(dir))
		return nil
	}
	d.logf("tearing down stack %s...", projectName(appName))
	if _, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker compose -p %s -f %s down", q(projectName(appName)), q(file))); err != nil {
		return fmt.Errorf("compose down: %w", err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "rm -rf "+q(dir)); err != nil {
		return fmt.Errorf("remove app dir %s: %w", dir, err)
	}
	return nil
}
