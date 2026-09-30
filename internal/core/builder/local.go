package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"easydrop/internal/models"
)

// registryPattern accepts an optional host[:port] and an optional namespace
// path, e.g. `ghcr.io`, `registry.example.com:5000`, `myorg/apps`. A scheme
// (`https://`) is rejected: docker refs never carry one, and silently dropping
// it would push to the wrong place.
var registryPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?(:[0-9]{1,5})?(/[A-Za-z0-9._-]+)*$`)

// imageNamePattern mirrors the docker image name charset for the repo part.
var imageNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// LocalBuilder implements the registry-based build strategy (FR-04,
// build.strategy = "local"): the image is built on the MACHINE RUNNING
// EasyDrop, pushed to the configured registry, and pulled by the target
// host. Nothing but the image crosses the wire — no workspace upload.
//
// Progress goes to Out (os.Stderr default).
type LocalBuilder struct {
	// SrcDir is the workspace to build; empty means the process cwd.
	SrcDir string
	Out    io.Writer
	// CommandTimeout bounds each docker invocation (default 30m — big builds).
	CommandTimeout time.Duration
}

// NewLocalBuilder creates a local (client-side) builder.
func NewLocalBuilder() *LocalBuilder {
	return &LocalBuilder{Out: os.Stderr}
}

func (b *LocalBuilder) logf(format string, args ...any) {
	fmt.Fprintf(b.Out, "easydrop: local-build: "+format+"\n", args...)
}

func (b *LocalBuilder) srcDir() (string, error) {
	if b.SrcDir != "" {
		return b.SrcDir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve workspace dir: %w", err)
	}
	return cwd, nil
}

func (b *LocalBuilder) timeout() time.Duration {
	if b.CommandTimeout > 0 {
		return b.CommandTimeout
	}
	return 30 * time.Minute
}

// runDocker executes a docker command in the workspace and streams its output
// to Out (so the user sees the build log live).
func (b *LocalBuilder) runDocker(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = ""
	if dir, err := b.srcDir(); err == nil {
		cmd.Dir = dir
	}
	cmd.Stdout = b.Out
	cmd.Stderr = b.Out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// TargetRef resolves the full registry reference: [registry/]<image>:<tag>.
// `build.image` may be `name`, `name:tag` or `ns/name:tag`; the registry is
// prepended unless the image already carries one.
func TargetRef(cfg *models.Config) (string, error) {
	registry := strings.TrimSuffix(strings.TrimSpace(cfg.Build.Registry), "/")
	if registry == "" {
		return "", fmt.Errorf("build.strategy = \"local\" requires build.registry (e.g. registry.example.com/myorg)")
	}
	if strings.Contains(registry, "://") {
		return "", fmt.Errorf("build.registry %q must not contain a scheme (use registry.example.com/myorg)", cfg.Build.Registry)
	}
	if !registryPattern.MatchString(registry) {
		return "", fmt.Errorf("invalid build.registry %q", cfg.Build.Registry)
	}

	image := strings.TrimSpace(cfg.Build.Image)
	if image == "" {
		image = cfg.App.Name
	}
	name, tag := image, "latest"
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i:], "/") {
		name, tag = image[:i], image[i+1:]
	}
	if name == "" {
		return "", fmt.Errorf("invalid build.image %q: repository name is empty", cfg.Build.Image)
	}
	if !imageNamePattern.MatchString(name) && !registryPattern.MatchString(name) {
		return "", fmt.Errorf("invalid build.image %q: must be a docker repository name", cfg.Build.Image)
	}
	if !imageNamePattern.MatchString(tag) && !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`).MatchString(tag) {
		return "", fmt.Errorf("invalid image tag %q", tag)
	}
	return registry + "/" + name + ":" + tag, nil
}

// checkLocalStaging fails fast when a snap-confined daemon cannot see the
// build context: such daemons run in a private mount namespace where /tmp is
// invisible, and `docker build` would die with an opaque
// "failed to read dockerfile" instead of a usable hint.
func (b *LocalBuilder) checkLocalStaging(ctx context.Context, srcDir string) error {
	out, err := exec.CommandContext(ctx, "docker",
		"info", "--format", "{{.DockerRootDir}}|{{.OperatingSystem}}").Output()
	if err != nil || !snapConfinedOutput(string(out)) {
		return nil
	}
	if strings.HasPrefix(srcDir, "/tmp/") {
		return fmt.Errorf("snap-confined docker daemon cannot see the build context in %q: move the project out of /tmp (e.g. under $HOME) — the same sandbox also hides /tmp for remote builds", srcDir)
	}
	return nil
}

// BuildAndPush builds the image locally, tags it for the registry and pushes
// it, returning the pushed reference. Requires build.registry.
func (b *LocalBuilder) BuildAndPush(ctx context.Context, app *models.Application) (string, error) {
	if app == nil || app.Config == nil {
		return "", fmt.Errorf("application config is nil")
	}
	cfg := app.Config
	if err := models.ValidateAppName(cfg.App.Name); err != nil {
		return "", err
	}
	ref, err := TargetRef(cfg)
	if err != nil {
		return "", err
	}
	srcDir, err := b.srcDir()
	if err != nil {
		return "", err
	}
	if err := b.checkLocalStaging(ctx, srcDir); err != nil {
		return "", err
	}

	buildCtx, cancel := context.WithTimeout(ctx, b.timeout())
	defer cancel()

	tag := ref[strings.LastIndex(ref, ":")+1:]
	localRef := ref
	b.logf("building %s (local)...", ref)
	args := []string{"build", "-t", localRef}
	if cfg.Build.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, ".")
	if err := b.runDocker(buildCtx, args...); err != nil {
		return "", fmt.Errorf("local build: %w", err)
	}
	b.logf("pushing %s...", ref)
	if err := b.runDocker(buildCtx, "push", localRef); err != nil {
		return "", fmt.Errorf("push to registry: %w", err)
	}
	b.logf("pushed %s (tag %s)", ref, tag)
	return ref, nil
}
