package bootstrapper

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"easydrop/internal/core"
)

// composeArch maps `uname -m` output to Docker Compose release asset names.
var composeArch = map[string]string{
	"x86_64":  "x86_64",
	"aarch64": "aarch64",
	"arm64":   "aarch64",
	"armv7l":  "armv7",
}

// composeTagPattern guards the resolved release tag before interpolating
// it into the download URL (injection safety).
var composeTagPattern = regexp.MustCompile(`^v[0-9A-Za-z._-]+$`)

// Bootstrapper prepares a target host: validates the OS, installs Docker
// Engine and the Compose plugin when missing, grants docker group access
// and aligns ufw rules. It works through a generic CommandExecutor, so the
// same code path covers local and remote hosts.
//
// Progress goes to Out (defaults to os.Stderr); tests can swap in a buffer.
type Bootstrapper struct {
	exec core.CommandExecutor
	Out  io.Writer
}

// New creates a Bootstrapper bound to the given executor.
func New(exec core.CommandExecutor) *Bootstrapper {
	return &Bootstrapper{exec: exec, Out: os.Stderr}
}

func (b *Bootstrapper) logf(format string, args ...any) {
	fmt.Fprintf(b.Out, "easydrop: bootstrap: "+format+"\n", args...)
}

// Bootstrap runs the full provisioning pipeline. It is idempotent:
// present components are probed and left untouched.
func (b *Bootstrapper) Bootstrap(ctx context.Context) error {
	if err := b.checkOS(ctx); err != nil {
		return err
	}
	if err := b.ensureDocker(ctx); err != nil {
		return err
	}
	if err := b.ensureCompose(ctx); err != nil {
		return err
	}
	if err := b.ensureDockerGroup(ctx); err != nil {
		return err
	}
	if err := b.ensureFirewall(ctx); err != nil {
		return err
	}
	b.logf("host ready")
	return nil
}

// checkOS halts on non-Debian-family systems. When the target is local and
// /etc/os-release is unreadable (macOS/Windows dev machine), it emits a soft
// warning to stderr and proceeds.
func (b *Bootstrapper) checkOS(ctx context.Context) error {
	stdout, _, _, err := b.exec.ExecCommand(ctx, "cat /etc/os-release")
	if err != nil {
		if _, ok := b.exec.(*core.LocalExecutor); ok {
			b.logf("warning: cannot read /etc/os-release (%v); assuming non-Linux local dev machine and proceeding", err)
			return nil
		}
		return fmt.Errorf("read /etc/os-release: %w", err)
	}
	id := parseOSReleaseID(stdout)
	b.logf("detected OS ID=%q", id)
	if id != "ubuntu" && id != "debian" {
		return fmt.Errorf("unsupported OS ID=%q: only ubuntu and debian are supported", id)
	}
	return nil
}

// parseOSReleaseID extracts the ID= value from os-release content.
func parseOSReleaseID(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID=") {
			return strings.ToLower(strings.Trim(strings.TrimPrefix(line, "ID="), `"'`))
		}
	}
	return ""
}

// ensureDocker installs Docker Engine via the official script when
// `docker --version` fails, then re-probes to confirm the install.
func (b *Bootstrapper) ensureDocker(ctx context.Context) error {
	if _, _, _, err := b.exec.ExecCommand(ctx, "docker --version"); err == nil {
		b.logf("docker engine present")
		return nil
	}
	b.logf("docker engine missing, installing via get.docker.com...")
	if _, _, _, err := b.exec.ExecCommand(ctx, "curl -fsSL https://get.docker.com -o get-docker.sh && sh get-docker.sh"); err != nil {
		return fmt.Errorf("install docker engine: %w", err)
	}
	if _, _, _, err := b.exec.ExecCommand(ctx, "docker --version"); err != nil {
		return fmt.Errorf("docker engine still missing after install: %w", err)
	}
	b.logf("docker engine installed")
	return nil
}

// ensureCompose installs the Compose plugin when `docker compose version`
// fails. The release tag is resolved at runtime from the GitHub latest
// redirect (no hardcoded version to go stale); the platform arch comes
// from `uname -m`.
func (b *Bootstrapper) ensureCompose(ctx context.Context) error {
	if _, _, _, err := b.exec.ExecCommand(ctx, "docker compose version"); err == nil {
		b.logf("docker compose present")
		return nil
	}
	b.logf("docker compose missing, installing from github.com/docker/compose...")

	archOut, _, _, err := b.exec.ExecCommand(ctx, "uname -m")
	if err != nil {
		return fmt.Errorf("detect arch via uname -m: %w", err)
	}
	arch, ok := composeArch[strings.TrimSpace(archOut)]
	if !ok {
		return fmt.Errorf("unsupported arch %q for docker compose (want one of x86_64, aarch64, armv7l)", strings.TrimSpace(archOut))
	}

	tagOut, _, _, err := b.exec.ExecCommand(ctx, "curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/docker/compose/releases/latest")
	if err != nil {
		return fmt.Errorf("resolve latest compose release: %w", err)
	}
	tag := tagOut[strings.LastIndex(tagOut, "/")+1:]
	if !composeTagPattern.MatchString(tag) {
		return fmt.Errorf("refusing to use suspicious compose tag %q", tag)
	}

	install := fmt.Sprintf(
		"mkdir -p ~/.docker/cli-plugins && curl -fsSL -o ~/.docker/cli-plugins/docker-compose https://github.com/docker/compose/releases/download/%s/docker-compose-linux-%s && chmod 0755 ~/.docker/cli-plugins/docker-compose",
		tag, arch,
	)
	if _, _, _, err := b.exec.ExecCommand(ctx, install); err != nil {
		return fmt.Errorf("install docker compose %s: %w", tag, err)
	}
	if _, _, _, err := b.exec.ExecCommand(ctx, "docker compose version"); err != nil {
		return fmt.Errorf("docker compose still missing after install: %w", err)
	}
	b.logf("docker compose %s installed (%s)", tag, arch)
	return nil
}

// ensureDockerGroup grants the current user access to the docker socket,
// skipping the privileged call when already a member (idempotent, and avoids
// pointless sudo on hosts where access is already arranged).
func (b *Bootstrapper) ensureDockerGroup(ctx context.Context) error {
	if out, _, _, err := b.exec.ExecCommand(ctx, "id -nG"); err == nil {
		for _, g := range strings.Fields(out) {
			if g == "docker" {
				b.logf("user already in docker group, skipping")
				return nil
			}
		}
	}
	if _, _, _, err := b.exec.ExecCommand(ctx, "sudo usermod -aG docker $USER"); err != nil {
		return fmt.Errorf("add user to docker group: %w", err)
	}
	return nil
}

// ensureFirewall aligns ufw rules when ufw is installed. Hosts without ufw
// are left untouched.
func (b *Bootstrapper) ensureFirewall(ctx context.Context) error {
	if _, _, _, err := b.exec.ExecCommand(ctx, "command -v ufw"); err != nil {
		b.logf("ufw not installed, skipping firewall provisioning")
		return nil
	}
	b.logf("provisioning ufw rules...")
	for _, cmd := range []string{
		"sudo ufw allow OpenSSH",
		"sudo ufw allow 'Nginx Full'",
		"sudo ufw --force enable",
	} {
		if _, _, _, err := b.exec.ExecCommand(ctx, cmd); err != nil {
			return fmt.Errorf("ufw provisioning step %q: %w", cmd, err)
		}
	}
	return nil
}
