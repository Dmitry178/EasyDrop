// Package deploy is the shared operation core behind both interfaces
// (CLI in internal/cli and MCP in internal/mcp): config loading, the full
// deploy pipeline, status/logs/rollback and project scaffolding.
package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"easydrop/internal/config"
	"easydrop/internal/core"
	"easydrop/internal/core/bootstrapper"
	"easydrop/internal/core/builder"
	"easydrop/internal/core/drivers"
	"easydrop/internal/core/infra"
	"easydrop/internal/models"
)

// Options carries per-run deploy settings (flags or MCP arguments).
type Options struct {
	ConfigPath    string
	NoCache       bool
	BlueGreen     bool
	SkipBootstrap bool
}

// LoadConfig parses the TOML deployment blueprint.
func LoadConfig(path string) (*models.Config, error) {
	return config.ParseConfig(path)
}

// ResolveAppName returns the explicit name or falls back to app.name.
func ResolveAppName(arg string, cfg *models.Config) (string, error) {
	if arg != "" {
		if err := models.ValidateAppName(arg); err != nil {
			return "", err
		}
		return arg, nil
	}
	if cfg == nil || cfg.App.Name == "" {
		return "", fmt.Errorf("no app name: pass an explicit name or set app.name in easydrop.toml")
	}
	return cfg.App.Name, nil
}

// ApplyFlags overlays per-run overrides onto the parsed config.
// Flags only force values on, never off (config-true stays true).
func ApplyFlags(cfg *models.Config, noCache, blueGreen bool) {
	if noCache {
		cfg.Build.NoCache = true
	}
	if blueGreen {
		cfg.Driver.BlueGreen = true
	}
}

// NewDriver builds the configured driver (single/compose/swarm), wiring
// ingress when a domain is set. srcDir feeds compose/swarm workspace
// shipping (single builds via RemoteBuilder in Run instead).
func NewDriver(cfg *models.Config, ex core.CommandExecutor, srcDir string) (drivers.DeploymentDriver, error) {
	var ingress drivers.IngressUpdater
	if cfg.Nginx.Domain != "" {
		ingress = infra.NewNginxManager(ex)
	}
	switch cfg.Driver.Type {
	case "single":
		sd := drivers.NewSingleDriver(ex)
		sd.BlueGreen = cfg.Driver.BlueGreen
		sd.Ingress = ingress
		return sd, nil
	case "compose":
		cd := drivers.NewComposeDriver(ex)
		cd.SrcDir = srcDir
		cd.Ingress = ingress
		return cd, nil
	case "swarm":
		wd := drivers.NewSwarmDriver(ex)
		wd.SrcDir = srcDir
		wd.Ingress = ingress
		return wd, nil
	default:
		return nil, fmt.Errorf("unknown driver type %q (want single, compose or swarm)", cfg.Driver.Type)
	}
}

// Run executes the full pipeline: executor → bootstrap → build (single
// driver only; compose/swarm ship the workspace themselves) → deploy (+
// ingress) → SSL. The workspace is the config file's directory.
func Run(ctx context.Context, opts Options, out func(string)) error {
	cfg, err := LoadConfig(opts.ConfigPath)
	if err != nil {
		return err
	}
	ApplyFlags(cfg, opts.NoCache, opts.BlueGreen)
	if cfg.Driver.BlueGreen && cfg.Driver.Type != "single" {
		return fmt.Errorf("blue-green is only supported by the single driver (got %q)", cfg.Driver.Type)
	}

	ex, err := core.NewExecutor(&cfg.Server)
	if err != nil {
		return err
	}
	defer ex.Close()

	app := &models.Application{Config: cfg}

	say := func(f string, a ...any) {
		if out != nil {
			out(fmt.Sprintf(f, a...))
		}
	}
	say("bootstrapping host...")
	if !opts.SkipBootstrap {
		if err := bootstrapper.New(ex).Bootstrap(ctx); err != nil {
			return err
		}
	} else {
		say("skipping host bootstrap (--skip-bootstrap)")
	}

	abs, err := filepath.Abs(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	srcDir := filepath.Dir(abs)

	drv, err := NewDriver(cfg, ex, srcDir)
	if err != nil {
		return err
	}
	if cfg.Driver.Type == "single" {
		switch cfg.Build.Strategy {
		case "local":
			// FR-04: build on THIS machine, push to the registry, and let
			// the host pull the image – no workspace upload at all.
			if err := buildLocalAndPull(ctx, cfg, app, ex, srcDir, out); err != nil {
				return err
			}
		default:
			rb := builder.NewRemoteBuilder(ex)
			rb.SrcDir = srcDir
			if err := rb.Build(ctx, app); err != nil {
				return err
			}
		}
	}
	if err := drv.Deploy(ctx, app); err != nil {
		return err
	}

	if cfg.Nginx.SSL && cfg.Nginx.Domain != "" {
		cm := infra.NewCertbotManager(ex, cfg.Server.Host)
		if err := cm.EnableSSL(ctx, cfg.Nginx.Domain, cfg.Nginx.Email); err != nil {
			return err
		}
	}

	say("%s deployed", cfg.App.Name)
	return nil
}

// buildLocalAndPull implements build.strategy = "local" for the single
// driver: LocalBuilder builds + pushes on this machine, the target pulls the
// pushed ref, and app.Image pins what the driver runs.
func buildLocalAndPull(ctx context.Context, cfg *models.Config, app *models.Application, ex core.CommandExecutor, srcDir string, out func(string)) error {
	if cfg.Driver.Type != "single" {
		return fmt.Errorf("build.strategy = \"local\" is only supported by the single driver (got %q): compose/swarm build their services on the host", cfg.Driver.Type)
	}
	lb := builder.NewLocalBuilder()
	lb.SrcDir = srcDir
	if out != nil {
		lb.Out = os.Stderr
	}
	ref, err := lb.BuildAndPush(ctx, app)
	if err != nil {
		return err
	}
	app.Image = ref
	if out != nil {
		out(fmt.Sprintf("pulling %s on the target host...", ref))
	}
	if _, _, _, err := ex.ExecCommand(ctx, "docker pull "+core.EscapeShellArg(ref)); err != nil {
		return fmt.Errorf("pull %s on target host (check registry auth and network reachability): %w", ref, err)
	}
	return nil
}

// Status queries the deployment state via the configured driver.
func Status(ctx context.Context, configPath, appName string) (*models.AppStatus, error) {
	cfg, ex, name, err := setupDriver(configPath, appName)
	if err != nil {
		return nil, err
	}
	defer ex.Close()
	drv, err := NewDriver(cfg, ex, "")
	if err != nil {
		return nil, err
	}
	return drv.Status(ctx, name)
}

// LogOptions carries the `logs` inputs both interfaces share.
type LogOptions struct {
	// Lines is the tail snapshot size (docker logs --tail).
	Lines int
	// Follow keeps streaming until ctx ends or Limit hits.
	Follow bool
	// Limit caps the number of *kept* lines; 0 means unlimited. Follow sets it
	// on both interfaces so a forgotten stream cannot grow without bound. It
	// counts lines that passed the filter, so a filter that never matches
	// still terminates (the driver channel closes when ctx ends).
	Limit int
	// Filter is a case-insensitive regular expression. Empty means no
	// filtering. Filtering happens after collection so it is identical for a
	// snapshot and a follow stream, and so a bad expression can never leave a
	// half-streamed channel behind.
	Filter string
}

// LogResult is the outcome of a log read: the lines that survived the filter
// plus the counts needed to tell "no errors" apart from "nothing collected".
type LogResult struct {
	// Lines are the kept lines, in collection order.
	Lines []string
	// Scanned is how many lines the driver produced before filtering.
	Scanned int
	// Matched is len(Lines) – the two are separate so a caller can report
	// "0 of 500 lines matched" instead of an empty answer.
	Matched int
}

// Logs collects container logs; follow streams until ctx ends or the limit hits.
// The filter is applied to the collected lines (see LogOptions.Filter).
func Logs(ctx context.Context, configPath, appName string, opts LogOptions) (*LogResult, error) {
	filter, err := compileLogFilter(opts.Filter)
	if err != nil {
		return nil, err
	}
	cfg, ex, name, err := setupDriver(configPath, appName)
	if err != nil {
		return nil, err
	}
	defer ex.Close()
	drv, err := NewDriver(cfg, ex, "")
	if err != nil {
		return nil, err
	}
	ch, err := drv.Logs(ctx, name, opts.Lines, opts.Follow)
	if err != nil {
		return nil, err
	}
	return collectLogs(ch, filter, opts.Limit), nil
}

// collectLogs drains ch, keeping the lines that match, and counts what it
// scanned. Separated from Logs so the filtering contract is testable without a
// live host, and so both interfaces get it by construction.
func collectLogs(ch <-chan string, filter *regexp.Regexp, limit int) *LogResult {
	res := &LogResult{}
	for line := range ch {
		res.Scanned++
		if !filter.MatchString(line) {
			continue
		}
		res.Lines = append(res.Lines, line)
		if limit > 0 && len(res.Lines) >= limit {
			break
		}
	}
	res.Matched = len(res.Lines)
	return res
}

// compileLogFilter turns the user expression into a matcher. An empty
// expression matches everything; an invalid one is an error rather than a
// silently-pass filter, which would look like "the container logged nothing".
func compileLogFilter(expr string) (*regexp.Regexp, error) {
	if strings.TrimSpace(expr) == "" {
		return regexp.MustCompile(""), nil
	}
	// (?i) makes the expression case-insensitive: log levels are spelled
	// ERROR, Error and error depending on the framework.
	re, err := regexp.Compile("(?i)" + expr)
	if err != nil {
		return nil, fmt.Errorf("invalid log filter %q: %w", expr, err)
	}
	return re, nil
}

// Rollback restores the backup kept by the last replacing deploy.
func Rollback(ctx context.Context, configPath, appName string) error {
	cfg, ex, name, err := setupDriver(configPath, appName)
	if err != nil {
		return err
	}
	defer ex.Close()
	cfg.App.Name = name // operate on the requested app
	drv, err := NewDriver(cfg, ex, "")
	if err != nil {
		return err
	}
	return drv.Rollback(ctx, &models.Application{Config: cfg})
}

// setupDriver loads config, resolves the app name and opens the executor.
func setupDriver(configPath, appName string) (*models.Config, core.CommandExecutor, string, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, nil, "", err
	}
	name, err := ResolveAppName(appName, cfg)
	if err != nil {
		return nil, nil, "", err
	}
	ex, err := core.NewExecutor(&cfg.Server)
	if err != nil {
		return nil, nil, "", err
	}
	return cfg, ex, name, nil
}

// Teardown removes the deployment: containers, the Blue-Green backup, the
// compose/swarm state dir and (compose) the stack. Volumes are never deleted –
// data outlives the deploy. Idempotent: nothing deployed is not an error.
func Teardown(ctx context.Context, configPath, appName string) error {
	cfg, ex, name, err := setupDriver(configPath, appName)
	if err != nil {
		return err
	}
	defer ex.Close()
	drv, err := NewDriver(cfg, ex, "")
	if err != nil {
		return err
	}
	return drv.Teardown(ctx, name)
}

// InitOptions carries the `init` inputs both interfaces share.
type InitOptions struct {
	// Force overwrites an existing easydrop.toml. Without it, Init refuses to
	// touch the file.
	Force bool
	// Port, when non-zero, overrides detection (OD-04).
	Port int
}

// Init scaffolds easydrop.toml in dir from its contents. The result carries the
// detected values, their sources and the shared Report() text, so the CLI and
// the MCP server present the very same outcome.
func Init(dir string, opts InitOptions) (*config.ScaffoldResult, error) {
	res, err := config.ScaffoldWith(dir, config.ScaffoldOptions{Port: opts.Port})
	if err != nil {
		return nil, err
	}
	if err := config.WriteConfig(filepath.Join(dir, "easydrop.toml"), res.Config, opts.Force); err != nil {
		return nil, err
	}
	return res, nil
}
