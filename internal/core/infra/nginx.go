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

var (
	nginxTemplate    = template.Must(template.New("nginx").Parse(templates.NginxConf))
	nginxTLSTemplate = template.Must(template.New("nginx-tls").Parse(templates.NginxTLSConf))
)

// Compile-time proof that NginxManager plugs into the driver layer.
var _ drivers.IngressUpdater = (*NginxManager)(nil)

// NginxManager renders and activates reverse-proxy configs on the target
// host. Privileged paths are never written directly: the rendered config is
// staged under the shared staging base and moved with `sudo mv`.
// Progress goes to Out (os.Stderr default).
type NginxManager struct {
	exec core.CommandExecutor
	// selfSigned switches rendering to the TLS template and makes Apply
	// guarantee the certificate exists *before* the config that references it is
	// tested. That ordering is the whole point: a `listen 443 ssl` block whose
	// files are missing fails `nginx -t`, and on a first deploy there is no
	// previous config to fall back to.
	selfSigned bool
	Out        io.Writer
}

// NewNginxManager creates a manager bound to the given executor.
func NewNginxManager(exec core.CommandExecutor) *NginxManager {
	return &NginxManager{exec: exec, Out: os.Stderr}
}

// NewNginxManagerTLS creates a manager that serves TLS from a locally generated
// self-signed certificate (M18).
func NewNginxManagerTLS(exec core.CommandExecutor) *NginxManager {
	return &NginxManager{exec: exec, selfSigned: true, Out: os.Stderr}
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
// A failed config test aborts before the reload – the previous live config
// keeps serving.
func (m *NginxManager) Apply(ctx context.Context, domain string, port int) error {
	if err := validateDomain(domain); err != nil {
		return err
	}
	if err := validatePort(port); err != nil {
		return err
	}

	// The certificate first, always. `nginx -t` validates that the files named by
	// `ssl_certificate` exist, so rendering before provisioning means the first
	// TLS deploy of a host fails with an nginx error about a file easydrop was
	// supposed to create two lines earlier.
	var cert CertResult
	if m.selfSigned {
		var err error
		if cert, err = NewSelfSignedCertifier(m.exec).EnsureCert(ctx, domain); err != nil {
			return fmt.Errorf("self-signed certificate for %s: %w", domain, err)
		}
	}

	rendered, err := m.render(domain, port, cert)
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
	if cert.CertPath != "" {
		m.reportTrust(ctx, domain, cert)
	}
	return nil
}

// render produces the vhost for the configured TLS mode.
func (m *NginxManager) render(domain string, port int, cert CertResult) (string, error) {
	data := map[string]any{"Domain": domain, "Port": port}
	tmpl := nginxTemplate
	if m.selfSigned {
		tmpl = nginxTLSTemplate
		data["CertPath"] = cert.CertPath
		data["KeyPath"] = cert.KeyPath
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render nginx template: %w", err)
	}
	return sb.String(), nil
}

// reportTrust tells the user how to make the browser stop warning.
//
// easydrop deliberately does NOT install the certificate into the system trust
// store: that needs sudo on the developer's machine and silently changes what
// every other program on it trusts, which is mkcert's decision to make
// interactively and explicitly – not something a deploy should do behind the
// user's back. So the command is printed instead of run. The two lines below are
// the entire difference between "TLS works but warns" and "TLS is trusted".
func (m *NginxManager) reportTrust(ctx context.Context, domain string, cert CertResult) {
	m.logf("self-signed certificate in use for %s (expires %s)", domain, cert.NotAfter.Format("2006-01-02"))
	if cert.Created {
		m.logf("the browser will warn until you trust it once:")
		m.logf("  sudo cp %s /usr/local/share/ca-certificates/easydrop-%s.crt && sudo update-ca-certificates",
			cert.CertPath, domain)
		m.logf("or verify from the shell with:  curl --cacert %s https://%s/", cert.CertPath, domain)
		return
	}
	m.logf("existing certificate reused – your browser trust decision still holds")
}

func q(s string) string { return core.EscapeShellArg(s) }
