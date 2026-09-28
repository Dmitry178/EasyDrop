package builder

import (
	"context"
	"fmt"
	"io"
	"os"

	"easydrop/internal/core"
	"easydrop/internal/models"
)

// RemoteBuilder ships the local workspace to the host and builds the image
// there (registry-free flow, FR-03). Progress goes to Out (os.Stderr default).
type RemoteBuilder struct {
	exec core.CommandExecutor
	// SrcDir is the workspace to archive; empty means the process cwd.
	SrcDir string
	Out    io.Writer
}

// NewRemoteBuilder creates a builder bound to the given executor.
func NewRemoteBuilder(exec core.CommandExecutor) *RemoteBuilder {
	return &RemoteBuilder{exec: exec, Out: os.Stderr}
}

func (b *RemoteBuilder) logf(format string, args ...any) {
	fmt.Fprintf(b.Out, "easydrop: build: "+format+"\n", args...)
}

// Build compresses the workspace, uploads it to
// /tmp/easydrop/builds/[appName]/ on the host, unpacks it, runs
// `docker build -t easydrop/[appName]:latest` (plus --no-cache when
// build.no_cache is set) and purges the remote staging dir on success.
// The local temp archive is always removed. On build failure the remote
// staging dir is intentionally kept for debugging.
func (b *RemoteBuilder) Build(ctx context.Context, app *models.Application) error {
	if app == nil || app.Config == nil {
		return fmt.Errorf("application config is nil")
	}
	name := app.Config.App.Name
	if err := models.ValidateAppName(name); err != nil {
		return err
	}

	srcDir := b.SrcDir
	if srcDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve workspace dir: %w", err)
		}
		srcDir = cwd
	}

	tmp, err := os.CreateTemp("", "easydrop-build-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp archive: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	b.logf("archiving workspace %s...", srcDir)
	if err := CreateProjectArchive(srcDir, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp archive: %w", err)
	}

	remoteDir := core.StagingBase() + "/builds/" + name
	remoteArchive := remoteDir + "/" + archiveFileName

	b.logf("uploading to %s...", remoteArchive)
	if err := b.exec.UploadFile(ctx, tmpPath, remoteArchive); err != nil {
		return fmt.Errorf("upload project archive: %w", err)
	}

	untar := fmt.Sprintf("tar -xzf %s -C %s",
		core.EscapeShellArg(remoteArchive), core.EscapeShellArg(remoteDir))
	if _, _, _, err := b.exec.ExecCommand(ctx, untar); err != nil {
		return fmt.Errorf("unpack project archive on host: %w", err)
	}

	buildCmd := fmt.Sprintf("docker build -t %s %s %s",
		core.EscapeShellArg("easydrop/"+name+":latest"),
		noCacheFlag(app.Config.Build.NoCache),
		core.EscapeShellArg(remoteDir),
	)
	b.logf("building image easydrop/%s:latest (no-cache=%v)...", name, app.Config.Build.NoCache)
	if _, _, _, err := b.exec.ExecCommand(ctx, buildCmd); err != nil {
		return fmt.Errorf("docker build on host (staging kept at %s for debugging): %w", remoteDir, err)
	}

	purge := fmt.Sprintf("rm -rf %s", core.EscapeShellArg(remoteDir))
	if _, _, _, err := b.exec.ExecCommand(ctx, purge); err != nil {
		return fmt.Errorf("purge remote staging dir %s: %w", remoteDir, err)
	}
	b.logf("image easydrop/%s:latest built", name)
	return nil
}

func noCacheFlag(noCache bool) string {
	if noCache {
		return "--no-cache"
	}
	return ""
}
