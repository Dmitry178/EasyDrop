package infra

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"easydrop/internal/core"
	"easydrop/internal/core/drivers"
	"easydrop/templates"
)

var nginxTemplate = template.Must(template.New("nginx").Parse(templates.NginxConf))

// Compile-time proof that NginxManager plugs into the driver layer.
var _ drivers.IngressUpdater = (*NginxManager)(nil)

// NginxManager renders and activates reverse-proxy configs on the target
// host. Privileged paths are never written directly: the rendered config is
// staged under the shared staging base and moved with `sudo mv`.
// Progress goes to Out (os.Stderr default).
type NginxManager struct {
	exec core.CommandExecutor
	Out  io.Writer
}

// NewNginxManager creates a manager bound to the given executor.
func NewNginxManager(exec core.CommandExecutor) *NginxManager {
	return &NginxManager{exec: exec, Out: os.Stderr}
}

func (m *NginxManager) logf(format string, args ...any) {
	fmt.Fprintf(m.Out, "easydrop: nginx: "+format+"\n", args...)
}

// UpdateIngress satisfies drivers.IngressUpdater: point `domain` at `port`.
func (m *NginxManager) UpdateIngress(ctx context.Context, domain string, port int) error {
	return m.Apply(ctx, domain, port)
}

// Apply renders the template for domain+port, stages it on the host, moves
// it into sites-available, links sites-enabled, then `nginx -t` + reload.
// A failed config test aborts before the reload — the previous live config
// keeps serving.
func (m *NginxManager) Apply(ctx context.Context, domain string, port int) error {
	if err := validateDomain(domain); err != nil {
		return err
	}
	if err := validatePort(port); err != nil {
		return err
	}

	rendered, err := renderNginx(domain, port)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "easydrop-nginx-*.conf")
	if err != nil {
		return fmt.Errorf("create temp nginx config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(rendered); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp nginx config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp nginx config: %w", err)
	}

	staged := core.StagingBase() + "/nginx/" + domain
	available := "/etc/nginx/sites-available/" + domain
	enabledDir := "/etc/nginx/sites-enabled/"

	m.logf("staging %s...", staged)
	if err := m.exec.UploadFile(ctx, tmpPath, staged); err != nil {
		return fmt.Errorf("stage nginx config: %w", err)
	}
	for _, step := range []string{
		fmt.Sprintf("sudo mv %s %s", q(staged), q(available)),
		fmt.Sprintf("sudo ln -sf %s %s", q(available), q(enabledDir)),
		"sudo nginx -t",
	} {
		if _, _, _, err := m.exec.ExecCommand(ctx, step); err != nil {
			return fmt.Errorf("nginx apply step %q: %w", step, err)
		}
	}
	if _, _, _, err := m.exec.ExecCommand(ctx, "sudo systemctl reload nginx"); err != nil {
		return fmt.Errorf("reload nginx: %w", err)
	}
	m.logf("%s -> 127.0.0.1:%d live", domain, port)
	return nil
}

func renderNginx(domain string, port int) (string, error) {
	var sb strings.Builder
	if err := nginxTemplate.Execute(&sb, map[string]any{"Domain": domain, "Port": port}); err != nil {
		return "", fmt.Errorf("render nginx template: %w", err)
	}
	return sb.String(), nil
}

func q(s string) string { return core.EscapeShellArg(s) }
