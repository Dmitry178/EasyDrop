package drivers

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"easydrop/internal/core"
	"easydrop/internal/core/builder"
	"easydrop/internal/models"
)

// SwarmDriver deploys clustered stacks via Docker Swarm (FR-07, MVP scope:
// single-node swarms). The workspace is shipped and service images are built
// on the node with `docker compose build`, then `docker stack deploy` runs.
// Multi-node clusters need images in a registry (Build.Registry) — out of
// MVP scope and reported explicitly. Progress goes to Out (os.Stderr default).
type SwarmDriver struct {
	exec core.CommandExecutor
	// SrcDir is the workspace to archive; empty means the process cwd.
	SrcDir string
	// Ingress is called after a successful deploy; nil skips the update.
	Ingress IngressUpdater
	Out     io.Writer
	// FollowInterval tunes the follow-poll loop in Logs (default 2s).
	FollowInterval time.Duration
	// homeDir caches resolveHome per driver instance.
	homeDir string
}

// NewSwarmDriver creates a driver bound to the given executor.
func NewSwarmDriver(exec core.CommandExecutor) *SwarmDriver {
	return &SwarmDriver{exec: exec, Out: os.Stderr}
}

func (d *SwarmDriver) logf(format string, args ...any) {
	fmt.Fprintf(d.Out, "easydrop: swarm: "+format+"\n", args...)
}

func (d *SwarmDriver) followInterval() time.Duration {
	if d.FollowInterval > 0 {
		return d.FollowInterval
	}
	return followPollInterval
}

// appDir resolves the persistent per-app dir, caching the remote home.
// See stateDir for the snap-confined layout.
func (d *SwarmDriver) appDir(ctx context.Context, appName string) (string, error) {
	if d.homeDir == "" {
		home, err := resolveHome(ctx, d.exec)
		if err != nil {
			return "", err
		}
		d.homeDir = home
	}
	return stateDir(d.exec, ctx, d.homeDir) + "/" + appName, nil
}

func (d *SwarmDriver) srcDir() (string, error) {
	if d.SrcDir != "" {
		return d.SrcDir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve workspace dir: %w", err)
	}
	return cwd, nil
}

// ensureSwarm joins (or creates) a single-node swarm. Multi-node joins are
// out of scope: swarm init without join tokens always creates a new
// single-node cluster, which is exactly the MVP target.
func (d *SwarmDriver) ensureSwarm(ctx context.Context) error {
	out, _, _, err := d.exec.ExecCommand(ctx, "docker info --format '{{.Swarm.LocalNodeState}}'")
	if err == nil && strings.TrimSpace(out) == "active" {
		d.logf("swarm already active")
		return nil
	}
	d.logf("initializing single-node swarm...")
	if _, _, _, err := d.exec.ExecCommand(ctx, "docker swarm init"); err != nil {
		return fmt.Errorf("init swarm: %w", err)
	}
	return nil
}

// Deploy ships the workspace, builds service images on the node, deploys the
// stack, verifies replica counts, persists the live file (+ backup) and
// purges staging.
func (d *SwarmDriver) Deploy(ctx context.Context, app *models.Application) error {
	if app == nil || app.Config == nil {
		return fmt.Errorf("application config is nil")
	}
	cfg := app.Config
	if err := models.ValidateAppName(cfg.App.Name); err != nil {
		return err
	}
	name, stack := cfg.App.Name, projectName(cfg.App.Name)
	composeFile := cfg.Driver.ComposeFile
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}
	srcDir, err := d.srcDir()
	if err != nil {
		return err
	}

	if err := d.ensureSwarm(ctx); err != nil {
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

	d.logf("building service images on the node...")
	if _, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker compose -f %s build", q(remoteCompose))); err != nil {
		return fmt.Errorf("compose build on node (staging kept at %s for debugging): %w", remoteDir, err)
	}
	d.logf("deploying stack %s...", stack)
	if _, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker stack deploy -c %s %s", q(remoteCompose), q(stack))); err != nil {
		return fmt.Errorf("stack deploy (staging kept at %s for debugging): %w", remoteDir, err)
	}
	if err := d.checkReplicas(ctx, stack); err != nil {
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
			return fmt.Errorf("backup live stack file: %w", err)
		}
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, fmt.Sprintf("cp -f %s %s", q(remoteCompose), q(live))); err != nil {
		return fmt.Errorf("persist live stack file: %w", err)
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
	d.logf("stack %s live", stack)
	return nil
}

// checkReplicas requires every stack service at full replica count (x/x).
func (d *SwarmDriver) checkReplicas(ctx context.Context, stack string) error {
	out, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker stack services %s --format '{{.Replicas}}'", q(stack)))
	if err != nil {
		return fmt.Errorf("query stack services: %w", err)
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return fmt.Errorf("stack %s reports no services", stack)
	}
	for _, l := range lines {
		parts := strings.SplitN(strings.TrimSpace(l), "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("unparsable replica count %q", l)
		}
		actual, err1 := strconv.Atoi(parts[0])
		desired, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || desired == 0 || actual != desired {
			return fmt.Errorf("stack service not at full replicas: %q", l)
		}
	}
	return nil
}

// liveStack resolves the stack name + persistent file, erroring when the app
// was never deployed.
func (d *SwarmDriver) liveStack(ctx context.Context, appName string) (stack, file string, err error) {
	dir, err := d.appDir(ctx, appName)
	if err != nil {
		return "", "", err
	}
	file = dir + "/compose.yml"
	if _, _, _, err := d.exec.ExecCommand(ctx, "test -f "+q(file)); err != nil {
		return "", "", fmt.Errorf("swarm deployment %q not found on host", appName)
	}
	return projectName(appName), file, nil
}

// Status aggregates replica counts: all x/x → Up, otherwise Down.
func (d *SwarmDriver) Status(ctx context.Context, appName string) (*models.AppStatus, error) {
	if err := models.ValidateAppName(appName); err != nil {
		return nil, err
	}
	stack, _, err := d.liveStack(ctx, appName)
	if err != nil {
		return &models.AppStatus{Status: "Down"}, nil
	}
	out, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker stack services %s --format '{{.Replicas}}'", q(stack)))
	if err != nil {
		return nil, fmt.Errorf("query stack services: %w", err)
	}
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return &models.AppStatus{Status: "Down"}, nil
	}
	for _, l := range lines {
		parts := strings.SplitN(strings.TrimSpace(l), "/", 2)
		if len(parts) != 2 {
			return &models.AppStatus{Status: "Down"}, nil
		}
		actual, err1 := strconv.Atoi(parts[0])
		desired, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || desired == 0 || actual != desired {
			return &models.AppStatus{Status: "Down"}, nil
		}
	}
	return &models.AppStatus{Status: "Up"}, nil
}

// serviceNames lists stack service names for log collection.
func (d *SwarmDriver) serviceNames(ctx context.Context, stack string) ([]string, error) {
	out, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker stack services %s --format '{{.Name}}'", q(stack)))
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// Logs concatenates `docker service logs` across stack services with
// `==> service <==` headers; follow polls until ctx ends.
func (d *SwarmDriver) Logs(ctx context.Context, appName string, lines int, follow bool) (<-chan string, error) {
	if err := models.ValidateAppName(appName); err != nil {
		return nil, err
	}
	if lines < 0 {
		lines = 0
	}
	stack, _, err := d.liveStack(ctx, appName)
	if err != nil {
		return nil, err
	}
	svcs, err := d.serviceNames(ctx, stack)
	if err != nil {
		return nil, fmt.Errorf("list stack services: %w", err)
	}
	ch := make(chan string, 100)
	go streamLines(ctx, func(ctx context.Context) (string, error) {
		var sb strings.Builder
		for _, svc := range svcs {
			out, _, _, err := d.exec.ExecCommand(ctx,
				fmt.Sprintf("docker service logs --tail %d %s", lines, q(svc)))
			if err != nil {
				return "", err
			}
			sb.WriteString("==> " + svc + " <==\n" + out)
		}
		return sb.String(), nil
	}, follow, d.followInterval(), ch)
	return ch, nil
}

// Rollback restores the previous stack file and redeploys (images are
// cached). It consumes the backup.
func (d *SwarmDriver) Rollback(ctx context.Context, app *models.Application) error {
	if app == nil || app.Config == nil {
		return fmt.Errorf("application config is nil")
	}
	cfg := app.Config
	if err := models.ValidateAppName(cfg.App.Name); err != nil {
		return err
	}
	name, stack := cfg.App.Name, projectName(cfg.App.Name)
	dir, err := d.appDir(ctx, name)
	if err != nil {
		return err
	}
	live, prev := dir+"/compose.yml", dir+"/compose.previous.yml"

	if _, _, _, err := d.exec.ExecCommand(ctx, "test -f "+q(prev)); err != nil {
		return fmt.Errorf("no rollback backup for %q (only swarm deploys that replaced a live stack keep one)", name)
	}
	d.logf("rolling back stack %s...", stack)
	if _, _, _, err := d.exec.ExecCommand(ctx, fmt.Sprintf("cp -f %s %s", q(prev), q(live))); err != nil {
		return fmt.Errorf("restore previous stack file: %w", err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx,
		fmt.Sprintf("docker stack deploy -c %s %s", q(live), q(stack))); err != nil {
		return fmt.Errorf("stack deploy after rollback: %w", err)
	}
	if err := d.checkReplicas(ctx, stack); err != nil {
		return fmt.Errorf("rollback health: %w", err)
	}
	_, _, _, _ = d.exec.ExecCommand(ctx, "rm -f "+q(prev))
	d.logf("stack %s rolled back", stack)
	return nil
}

// Teardown removes the stack and the persisted app dir. Missing stacks are
// not an error.
func (d *SwarmDriver) Teardown(ctx context.Context, appName string) error {
	if err := models.ValidateAppName(appName); err != nil {
		return err
	}
	dir, err := d.appDir(ctx, appName)
	if err != nil {
		return err
	}
	stack := projectName(appName)
	out, _, _, err := d.exec.ExecCommand(ctx, "docker stack ls --format '{{.Name}}'")
	if err != nil {
		return fmt.Errorf("list stacks: %w", err)
	}
	found := false
	for _, l := range nonEmptyLines(out) {
		if strings.TrimSpace(l) == stack {
			found = true
		}
	}
	if !found {
		d.logf("stack %s not present, skipping", stack)
		_, _, _, _ = d.exec.ExecCommand(ctx, "rm -rf "+q(dir))
		return nil
	}
	d.logf("removing stack %s...", stack)
	if _, _, _, err := d.exec.ExecCommand(ctx, "docker stack rm "+q(stack)); err != nil {
		return fmt.Errorf("remove stack %s: %w", stack, err)
	}
	if _, _, _, err := d.exec.ExecCommand(ctx, "rm -rf "+q(dir)); err != nil {
		return fmt.Errorf("remove app dir %s: %w", dir, err)
	}
	return nil
}
