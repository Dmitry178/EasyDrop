package builder

import (
	"context"
	"fmt"
	"os"
	"strings"

	"easydrop/internal/core"
)

// ArchiveWorkspace compresses srcDir into a temp tar.gz file and returns its
// path. The caller must remove the file when done.
func ArchiveWorkspace(srcDir string) (string, error) {
	tmp, err := os.CreateTemp("", "easydrop-build-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("create temp archive: %w", err)
	}
	tmpPath := tmp.Name()
	if err := CreateProjectArchive(srcDir, tmp); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("close temp archive: %w", err)
	}
	return tmpPath, nil
}

// StageWorkspace uploads a local tar.gz archive to remoteDir on the host and
// unpacks it there (parent dirs are created by UploadFile). It fails fast on
// snap-confined daemons paired with /tmp staging (OD-02): they cannot see
// the host /tmp, so any later `docker build` would die cryptically.
func StageWorkspace(ctx context.Context, ex core.CommandExecutor, archivePath, remoteDir string) error {
	if err := checkDaemonStaging(ctx, ex); err != nil {
		return err
	}
	remoteArchive := remoteDir + "/" + archiveFileName
	if err := ex.UploadFile(ctx, archivePath, remoteArchive); err != nil {
		return fmt.Errorf("upload project archive: %w", err)
	}
	untar := fmt.Sprintf("tar -xzf %s -C %s",
		core.EscapeShellArg(remoteArchive), core.EscapeShellArg(remoteDir))
	if _, _, _, err := ex.ExecCommand(ctx, untar); err != nil {
		return fmt.Errorf("unpack project archive on host: %w", err)
	}
	return nil
}

// snapConfinedOutput reports whether `docker info` output marks a
// snap-confined daemon. Shared by the remote staging check and the local
// builder (both hit the same sandbox limits).
func snapConfinedOutput(infoOut string) bool {
	return strings.Contains(infoOut, "/var/snap/docker") || strings.Contains(infoOut, "Ubuntu Core")
}

// IsSnapConfinedDaemon reports whether the target daemon runs inside a snap
// sandbox. Such daemons see neither host /tmp nor hidden files in $HOME
// (snapd's home interface), which affects staging paths and state dirs.
// Unparsable `docker info` output reports false – the downstream docker call
// then fails loudly on its own.
func IsSnapConfinedDaemon(ctx context.Context, ex core.CommandExecutor) bool {
	out, _, _, err := ex.ExecCommand(ctx, "docker info --format '{{.DockerRootDir}}|{{.OperatingSystem}}'")
	if err != nil {
		return false
	}
	return snapConfinedOutput(out)
}

// checkDaemonStaging detects snap-confined dockerd (private /tmp) combined
// with /tmp-based staging and aborts with an actionable message.
func checkDaemonStaging(ctx context.Context, ex core.CommandExecutor) error {
	if !IsSnapConfinedDaemon(ctx, ex) {
		return nil
	}
	if strings.HasPrefix(core.StagingBase(), "/tmp/") {
		return fmt.Errorf("snap-confined docker daemon cannot see host /tmp staging at %q: set EASYDROP_STAGING_BASE to a daemon-visible directory – a NON-hidden path under $HOME (e.g. ~/easydrop-staging), since snapd also hides dot-directories in $HOME",
			core.StagingBase()+"/builds")
	}
	return nil
}
