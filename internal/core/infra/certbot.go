package infra

import (
	"context"
	"fmt"
	"io"
	"os"

	"easydrop/internal/core"
)

// CertbotManager provisions Let's Encrypt certificates via the certbot
// --nginx plugin on the target host. Progress goes to Out (os.Stderr default).
type CertbotManager struct {
	exec core.CommandExecutor
	// serverHost is the deploy target; localhost targets skip provisioning
	// (Let's Encrypt cannot validate local endpoints).
	serverHost string
	Out        io.Writer
}

// NewCertbotManager creates a manager bound to the executor and target host.
func NewCertbotManager(exec core.CommandExecutor, serverHost string) *CertbotManager {
	return &CertbotManager{exec: exec, serverHost: serverHost, Out: os.Stderr}
}

func (m *CertbotManager) logf(format string, args ...any) {
	fmt.Fprintf(m.Out, "easydrop: certbot: "+format+"\n", args...)
}

// EnableSSL provisions (or renews, idempotently) a certificate for domain.
// Localhost targets succeed immediately without doing anything. An empty
// email provisions with --register-unsafely-without-email.
func (m *CertbotManager) EnableSSL(ctx context.Context, domain, email string) error {
	if err := validateDomain(domain); err != nil {
		return err
	}
	if isLocalHost(m.serverHost) {
		m.logf("localhost target, skipping certificate provisioning for %s", domain)
		return nil
	}
	cmd := fmt.Sprintf("sudo certbot --nginx -d %s --non-interactive --agree-tos", q(domain))
	if email == "" {
		m.logf("no email configured, registering without email for %s", domain)
		cmd += " --register-unsafely-without-email"
	} else {
		cmd += " --email " + q(email)
	}
	if _, _, _, err := m.exec.ExecCommand(ctx, cmd); err != nil {
		return fmt.Errorf("provision certificate for %s: %w", domain, err)
	}
	m.logf("certificate live for %s", domain)
	return nil
}
