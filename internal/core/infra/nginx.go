package infra

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"
	"time"

	"easydrop/internal/core"
	"easydrop/internal/core/drivers"
	"easydrop/templates"
)

var (
	nginxTemplate    = template.Must(template.New("nginx").Parse(templates.NginxConf))
	nginxTLSTemplate = template.Must(template.New("nginx-tls").Parse(templates.NginxTLSConf))
)

// Managed nginx paths. easydrop owns exactly these two files per domain and
// nothing else under /etc/nginx – which is what makes removing them safe.
const (
	nginxAvailable = "/etc/nginx/sites-available/"
	nginxEnabled   = "/etc/nginx/sites-enabled/"
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
	available := nginxAvailable + domain

	m.logf("staging %s...", staged)
	if err := m.exec.UploadFile(ctx, tmpPath, staged); err != nil {
		return fmt.Errorf("stage nginx config: %w", err)
	}
	for _, step := range []string{
		fmt.Sprintf("sudo mv %s %s", q(staged), q(available)),
		fmt.Sprintf("sudo ln -sf %s %s", q(available), q(nginxEnabled)),
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
		m.reportTrust(domain, cert)
	}
	return nil
}

// IngressRemoval reports what RemoveIngress actually did, so the caller can be
// honest about a no-op instead of claiming a removal that never happened.
type IngressRemoval struct {
	// Removed is false when there was no vhost for this domain – the idempotent
	// case, and the one that decides which message the user gets.
	Removed bool
	// Reloaded is false when nginx could not be reloaded. The running nginx then
	// keeps serving the vhost it had already loaded, which the caller must say
	// rather than let the user assume the site is gone.
	Reloaded bool
	// ReloadReason explains a Reloaded=false, empty when reloading succeeded.
	ReloadReason string
}

// RemoveIngress deletes the managed vhost for domain and reloads nginx.
//
// Removing it by default is the point. A vhost left behind after `teardown` is
// not a clean state: nginx keeps proxying to a port with no container on it, so
// the domain answers 502 instead of refusing the connection – the deployment
// looks half-alive, and nothing in the output says why. easydrop wrote both
// files (Apply overwrites them on every deploy), so it owns exactly them and
// nothing else under /etc/nginx.
//
// The reload is guarded the same way Apply guards it: if `nginx -t` fails we
// return without reloading, because reloading a broken config takes down every
// other site on the host. The files are already gone at that point, so the
// running nginx is still serving the old vhost from memory – the caller has to
// report that rather than claim success.
func (m *NginxManager) RemoveIngress(ctx context.Context, domain string) (IngressRemoval, error) {
	if err := validateDomain(domain); err != nil {
		return IngressRemoval{}, err
	}
	available := nginxAvailable + domain
	enabled := nginxEnabled + domain

	// The enabled symlink first: it is the one nginx actually reads, so removing
	// the real file first would leave the symlink dangling and nginx -t failing
	// for a reason that has nothing to do with the user's config.
	if _, _, _, err := m.exec.ExecCommand(ctx, "sudo test -e "+q(available)+" -o -e "+q(enabled)); err != nil {
		m.logf("no nginx vhost for %s, nothing to remove", domain)
		return IngressRemoval{}, nil
	}
	for _, step := range []string{
		"sudo rm -f " + q(enabled),
		"sudo rm -f " + q(available),
	} {
		if _, _, _, err := m.exec.ExecCommand(ctx, step); err != nil {
			return IngressRemoval{}, fmt.Errorf("remove nginx vhost step %q: %w", step, err)
		}
	}
	m.logf("removed nginx vhost %s (%s)", domain, available)

	if _, _, _, err := m.exec.ExecCommand(ctx, "sudo nginx -t"); err != nil {
		reason := fmt.Sprintf("nginx config test failed: %v", err)
		m.logf("%s – not reloading, nginx keeps its current config", reason)
		return IngressRemoval{Removed: true, ReloadReason: reason}, nil
	}
	if _, _, _, err := m.exec.ExecCommand(ctx, "sudo systemctl reload nginx"); err != nil {
		reason := fmt.Sprintf("reload failed: %v", err)
		m.logf("%s – nginx keeps its current config", reason)
		return IngressRemoval{Removed: true, ReloadReason: reason}, nil
	}
	m.logf("nginx reloaded, %s no longer served", domain)
	return IngressRemoval{Removed: true, Reloaded: true}, nil
}

// TLSStatus reports what nginx is actually serving for domain, read from the
// running configuration (`nginx -T`) rather than from easydrop's config.
//
// The distinction matters. `nginx.ssl = true` says what easydrop was asked to
// do; `nginx -T` says what is loaded. They diverge in exactly the situations a
// user needs to be told about: certbot rewrote the vhost after the deploy, the
// vhost was removed by something else, or the certificate could not be issued.
// Reporting the configured intent would be a claim easydrop cannot back.
//
// Everything reported here – the certificate's kind, its expiry, whether it
// covers this domain – comes from the certificate file nginx itself points at,
// so it is identical in both modes (M20). It used to be asymmetric: a
// self-signed target reported an expiry date while a Let's Encrypt target
// reported only "TLS", even though `nginx -T` names the certificate path and
// nothing more was needed to read it.
//
// Returns "" when there is no vhost, when TLS is not being served for it, or
// when nginx cannot be asked – "no answer" rather than "no TLS".
func (m *NginxManager) TLSStatus(ctx context.Context, domain string) string {
	if err := validateDomain(domain); err != nil {
		return ""
	}
	out, _, _, err := m.exec.ExecCommand(ctx, "sudo nginx -T")
	if err != nil {
		// A host without nginx, or without permission to read its config, is a
		// legitimate state – status must not fail over a cosmetic detail.
		return ""
	}
	block := nginxBlockFor(out, domain)
	if block == "" {
		return ""
	}
	if !servesTLS(block) {
		return ""
	}
	certPath := sslCertificatePath(block)
	if certPath == "" {
		return "TLS (certificate path not found in the vhost)"
	}
	cert, err := m.readCert(ctx, certPath)
	if err != nil {
		return fmt.Sprintf("TLS (certificate at %s unreadable: %v)", certPath, err)
	}
	return describeCert(cert, domain)
}

// readCert reads and parses the certificate nginx serves. A fullchain file
// holds several PEM blocks; the first is the leaf, and the leaf is the one whose
// expiry the client actually notices.
func (m *NginxManager) readCert(ctx context.Context, path string) (*x509.Certificate, error) {
	out, _, _, err := m.exec.ExecCommand(ctx, "sudo cat "+q(path))
	if err != nil {
		return nil, err
	}
	rest := []byte(out)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil, fmt.Errorf("no PEM block found")
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(blk.Bytes)
	}
}

// describeCert renders the one line `status` prints. The kind is derived from
// the certificate itself (issuer == subject means self-signed) rather than from
// easydrop's config, so it cannot mislabel a domain certbot re-issued or a
// config switched after the last deploy.
func describeCert(cert *x509.Certificate, domain string) string {
	kind := "TLS"
	if bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		kind = "self-signed"
	}
	status := fmt.Sprintf("%s, expires %s", kind, cert.NotAfter.Format("2006-01-02"))
	if time.Now().Add(renewBefore).After(cert.NotAfter) {
		status += " EXPIRING"
	}
	// A certificate that does not cover the domain it is served under breaks in
	// a way that looks like nothing at all: nginx starts happily, the deploy
	// healthchecks green, and only the client complains. This is the case that
	// motivated reading the file rather than trusting the config.
	if !namesDomain(cert, domain) {
		status += fmt.Sprintf(" – DOES NOT COVER %s", domain)
	}
	return status
}

// sslCertificatePath extracts the first `ssl_certificate <path>;` from a vhost.
func sslCertificatePath(block string) string {
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "ssl_certificate" {
			continue
		}
		if p := strings.TrimSuffix(fields[1], ";"); p != "" {
			return p
		}
	}
	return ""
}

// nginxBlockFor extracts the configuration block nginx loaded for domain.
// `nginx -T` prints the whole config, each file introduced by a
// `# configuration file <path>:` comment, so the block is the text between that
// header and the next one.
func nginxBlockFor(dump, domain string) string {
	const header = "# configuration file "
	var block strings.Builder
	inBlock := false
	for _, line := range strings.Split(dump, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, header) {
			path := strings.TrimSuffix(strings.TrimPrefix(trimmed, header), ":")
			// Only the domain's own vhost counts. A wildcard `default_server` or
			// another site's block says nothing about this domain.
			inBlock = strings.TrimSuffix(strings.TrimSpace(path), "/") == nginxAvailable+domain
			block.Reset()
			continue
		}
		if inBlock {
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}
	return block.String()
}

// servesTLS reports whether a vhost block terminates TLS on 443.
//
// The field parsing is deliberately loose: nginx accepts `listen 443 ssl;`,
// `listen 443 ssl http2;` and `listen [::]:443 ssl;`, and this only has to tell
// "is 443 served with TLS" from "it is not". Anything else (a commented line, a
// different port) must read as no TLS, because a false positive here would tell
// a user their app is behind HTTPS when it is not.
func servesTLS(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "listen" {
			continue
		}
		ssl, on443 := false, false
		for _, f := range fields[1:] {
			f = strings.TrimSuffix(f, ";")
			if f == "ssl" {
				ssl = true
				continue
			}
			if listenPort(f) == "443" {
				on443 = true
			}
		}
		if ssl && on443 {
			return true
		}
	}
	return false
}

// listenPort extracts the port from a listen argument: "443", "0.0.0.0:443" or
// "[::]:443" all yield 443, while a modifier like "http2" yields "".
func listenPort(arg string) string {
	if i := strings.LastIndex(arg, ":"); i >= 0 {
		arg = arg[i+1:]
	}
	for _, r := range arg {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return arg
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
func (m *NginxManager) reportTrust(domain string, cert CertResult) {
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
