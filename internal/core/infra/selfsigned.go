package infra

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"easydrop/internal/core"
	"easydrop/internal/models"
)

// Self-signed certificate parameters.
//
// certDir is the only writable-by-nginx location easydrop owns. It is on the
// host where nginx runs, because the certificate is consumed by nginx there and
// by nothing else – shipping it anywhere else would mean shipping a private key
// for no reason.
//
// renewBefore is what makes the feature tolerable rather than maddening. A
// certificate regenerated on every deploy changes its fingerprint, so the
// browser warning the user already learned to click through reappears on every
// single deploy. Rotating only when there is less than a month left means a
// normal deploy never touches it, and the rotation is rare enough to be a
// one-time re-trust.
const (
	certDir      = "/etc/nginx/ssl"
	renewBefore  = 30 * 24 * time.Hour
	certValidity = 90 * 24 * time.Hour
	// maxSANs bounds the loopback SANs added to a hostname certificate. Two is
	// the real number (127.0.0.1 and ::1); the cap is a guard against a future
	// edit adding unbounded IPs to every certificate.
	maxSANs = 2
)

// CertResult reports where a certificate ended up and whether it was created
// now. Created=false means an existing, still-valid certificate was kept, which
// the caller surfaces to the user because it is the difference between "your
// trust decision still holds" and "re-trust this".
type CertResult struct {
	CertPath string
	KeyPath  string
	Created  bool
	// NotAfter is when the certificate expires, so the message can say when the
	// user will be asked to trust a new one.
	NotAfter time.Time
}

// certOptions are the generation inputs, separated from the host interaction so
// the crypto is testable without a target.
type certOptions struct {
	domain   string
	validFor time.Duration
	now      time.Time // zero = time.Now
}

// certPaths returns the on-host locations for a domain's certificate.
func certPaths(domain string) (certPath, keyPath, dir string) {
	return certDir + "/" + domain + ".crt", certDir + "/" + domain + ".key", certDir
}

// SelfSignedCertifier ensures a locally generated leaf certificate exists on the
// target host. Progress goes to Out (os.Stderr default).
type SelfSignedCertifier struct {
	exec core.CommandExecutor
	Out  io.Writer
}

// NewSelfSignedCertifier creates a certifier bound to the executor.
func NewSelfSignedCertifier(exec core.CommandExecutor) *SelfSignedCertifier {
	return &SelfSignedCertifier{exec: exec, Out: os.Stderr}
}

func (c *SelfSignedCertifier) logf(format string, a ...any) {
	fmt.Fprintf(c.Out, "easydrop: self-signed: "+format+"\n", a...)
}

// EnsureCert guarantees a usable certificate for domain on the host and returns
// where it is. It is idempotent: a valid certificate naming the same host is
// left alone.
//
// The validation happens before any command runs. The domain reaches a shell
// command and a filesystem path, so an unchecked value here would be a command
// injection – the config parser rejects it earlier, but this function is
// exported and must not depend on its caller having done that.
func (c *SelfSignedCertifier) EnsureCert(ctx context.Context, domain string) (CertResult, error) {
	if err := models.ValidateCertifiableDomain(domain); err != nil {
		return CertResult{}, err
	}
	certPath, keyPath, dir := certPaths(domain)

	if _, _, _, err := c.exec.ExecCommand(ctx, "sudo mkdir -p "+q(dir)); err != nil {
		return CertResult{}, fmt.Errorf("create %s: %w", dir, err)
	}

	if existing, err := c.readCert(ctx, certPath); err == nil && existing != nil {
		if usable(existing, domain, time.Now()) {
			c.logf("reusing certificate for %s (valid until %s)", domain, existing.NotAfter.Format("2006-01-02"))
			return CertResult{CertPath: certPath, KeyPath: keyPath, NotAfter: existing.NotAfter}, nil
		}
		c.logf("rotating certificate for %s (expired or renamed)", domain)
	}

	certPEM, keyPEM, notAfter, err := generateSelfSigned(certOptions{domain: domain, validFor: certValidity})
	if err != nil {
		return CertResult{}, err
	}
	if err := c.upload(ctx, certPath, certPEM, 0644); err != nil {
		return CertResult{}, err
	}
	if err := c.upload(ctx, keyPath, keyPEM, 0600); err != nil {
		return CertResult{}, err
	}
	c.logf("certificate for %s generated (valid until %s)", domain, notAfter.Format("2006-01-02"))
	return CertResult{CertPath: certPath, KeyPath: keyPath, Created: true, NotAfter: notAfter}, nil
}

// readCert returns the existing certificate, or nil when there is none. A
// missing file is the normal first-deploy case, not an error; anything else
// (unreadable, malformed) degrades to "regenerate", because refusing to deploy
// over an unreadable certificate file would be worse than replacing it.
func (c *SelfSignedCertifier) readCert(ctx context.Context, path string) (*x509.Certificate, error) {
	if _, _, _, err := c.exec.ExecCommand(ctx, "sudo test -f "+q(path)); err != nil {
		return nil, nil
	}
	out, _, _, err := c.exec.ExecCommand(ctx, "sudo cat "+q(path))
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode([]byte(out))
	if blk == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return x509.ParseCertificate(blk.Bytes)
}

// upload stages a file through the shared staging base and moves it into place
// with `sudo mv`, the same privileged-path discipline as the nginx config
// (SFTP cannot write to /etc/nginx/ssl). mode is applied after the move because
// the staging directory is not the destination's permissions.
func (c *SelfSignedCertifier) upload(ctx context.Context, dest string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp("", "easydrop-tls-*")
	if err != nil {
		return fmt.Errorf("create temp certificate file: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp certificate file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp certificate file: %w", err)
	}

	staged := core.StagingBase() + "/tls/" + filepath.Base(dest)
	if err := c.exec.UploadFile(ctx, path, staged); err != nil {
		return fmt.Errorf("stage %s: %w", dest, err)
	}
	if _, _, _, err := c.exec.ExecCommand(ctx, fmt.Sprintf("sudo mv %s %s", q(staged), q(dest))); err != nil {
		return fmt.Errorf("install %s: %w", dest, err)
	}
	if _, _, _, err := c.exec.ExecCommand(ctx, fmt.Sprintf("sudo chmod %04o %s", mode, q(dest))); err != nil {
		return fmt.Errorf("chmod %s %s: %w", mode, dest, err)
	}
	return nil
}

// usable reports whether an existing certificate may be kept: it must still be
// valid past the renewal window and must name this host.
//
// The name check is not cosmetic. Renaming the domain in easydrop.toml while the
// certificate on disk still names the old one produces a TLS config that nginx
// accepts and no client can verify – a 502-shaped failure with a certificate
// warning on top.
func usable(cert *x509.Certificate, domain string, now time.Time) bool {
	if now.Add(renewBefore).After(cert.NotAfter) {
		return false
	}
	return namesDomain(cert, domain)
}

// namesDomain reports whether the certificate's SANs cover domain. Only the SANs
// are consulted: CN is ignored on purpose, because it is ignored by every modern
// verifier too, so a certificate whose identity lives only in CN would pass here
// and fail in the browser.
func namesDomain(cert *x509.Certificate, domain string) bool {
	for _, n := range cert.DNSNames {
		if n == domain {
			return true
		}
	}
	if ip := net.ParseIP(domain); ip != nil {
		for _, got := range cert.IPAddresses {
			if got.Equal(ip) {
				return true
			}
		}
	}
	return false
}

// generateSelfSigned produces a PEM leaf certificate and its private key.
//
// Pure Go on purpose: shelling out to `openssl` would add a host dependency for
// something the binary can do in a millisecond, and on a remote target that
// dependency would have to be installed first. The key never leaves this
// process except to be written straight to the host.
//
// P-256 rather than Ed25519: Ed25519 is newer and shorter, but a slice of the
// TLS ecosystem – older JDKs, some Go and Java HTTP clients, embedded stacks –
// will not negotiate it. ECDSA P-256 with SHA-256 is the widest-supported
// modern choice.
func generateSelfSigned(opts certOptions) (certPEM, keyPEM []byte, notAfter time.Time, err error) {
	if err := models.ValidateCertifiableDomain(opts.domain); err != nil {
		return nil, nil, time.Time{}, err
	}
	now := opts.now
	if now.IsZero() {
		now = time.Now()
	}
	validFor := opts.validFor
	if validFor <= 0 {
		validFor = certValidity
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("generate private key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("generate serial number: %w", err)
	}

	notBefore := now.Add(-time.Hour) // tolerate a little clock skew on the host
	notAfter = now.Add(validFor)

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: opts.domain,
			// The Org line is what makes the certificate identifiable in a trust
			// dialog – without it the user sees a bare hostname and has no way to
			// tell an easydrop certificate from anything else named "localhost".
			Organization: []string{"EasyDrop (self-signed)"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	setSANs(tmpl, opts.domain)

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("marshal private key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, notAfter, nil
}

// setSANs fills SubjectAltName, the only field modern verifiers read.
//
// A hostname certificate also gets the loopback addresses. That is
// convenience, not correctness: a developer hitting https://localhost reaches
// the vhost whose server_name is "localhost", but their browser may have
// resolved that name to ::1 first. Without the IP SANs the connection fails on
// the certificate rather than on anything the user did.
func setSANs(tmpl *x509.Certificate, domain string) {
	if ip := net.ParseIP(domain); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
		return
	}
	tmpl.DNSNames = []string{domain}
	if isLoopbackName(domain) {
		loopbacks := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
		if len(loopbacks) > maxSANs {
			loopbacks = loopbacks[:maxSANs]
		}
		tmpl.IPAddresses = loopbacks
	}
}

// isLoopbackName reports whether a hostname is served from this machine, which
// is the only case where the loopback SANs are truthful. Adding them to
// "example.com" would be a claim in the certificate that easydrop cannot verify.
//
// The *.localhost zone is included because RFC 6761 reserves it for loopback: a
// name under it can only ever mean this machine, so the SAN is a fact rather
// than a guess. A private name in /etc/hosts (api.local.test) is deliberately
// excluded – easydrop does not resolve DNS, so it cannot know where that name
// points, and claiming 127.0.0.1 for it would be a guess baked into a
// certificate.
func isLoopbackName(domain string) bool {
	d := strings.ToLower(domain)
	switch d {
	case "localhost", "localhost.localdomain", "ip6-localhost", "ip6-loopback":
		return true
	}
	return strings.HasSuffix(d, ".localhost")
}
