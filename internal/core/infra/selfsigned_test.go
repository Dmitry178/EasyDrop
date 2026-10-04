package infra

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func pemBlocks(t *testing.T, data string) []*pem.Block {
	t.Helper()
	var blocks []*pem.Block
	rest := []byte(data)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return blocks
		}
		blocks = append(blocks, blk)
	}
}

func mustGenerate(t *testing.T, opts certOptions) (certPEM, keyPEM string) {
	t.Helper()
	c, k, _, err := generateSelfSigned(opts)
	if err != nil {
		t.Fatalf("generateSelfSigned: %v", err)
	}
	return string(c), string(k)
}

func mustGenerateCert(t *testing.T, domain string, validFor time.Duration) string {
	t.Helper()
	c, _, _, err := generateSelfSigned(certOptions{domain: domain, validFor: validFor})
	if err != nil {
		t.Fatal(err)
	}
	return string(c)
}

func parseLeaf(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	blk, _ := pem.Decode([]byte(certPEM))
	if blk == nil {
		t.Fatal("no PEM block")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cert
}

func TestGenerateSelfSignedShape(t *testing.T) {
	certPEM, keyPEM := mustGenerate(t, certOptions{domain: "localhost", validFor: 24 * time.Hour})

	blocks := pemBlocks(t, certPEM)
	if len(blocks) != 1 || blocks[0].Type != "CERTIFICATE" {
		t.Fatalf("cert PEM must hold exactly one CERTIFICATE block, got %d", len(blocks))
	}
	keyBlocks := pemBlocks(t, keyPEM)
	if len(keyBlocks) != 1 {
		t.Fatalf("key PEM must hold exactly one block, got %d", len(keyBlocks))
	}
	if keyBlocks[0].Type != "EC PRIVATE KEY" {
		t.Errorf("unexpected key block type %q", keyBlocks[0].Type)
	}

	cert := parseLeaf(t, certPEM)
	// A certificate no modern client accepts is worse than no certificate.
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		t.Errorf("key algorithm = %v, want ECDSA (Ed25519 is refused by older TLS stacks)", cert.PublicKeyAlgorithm)
	}
	if got := cert.SignatureAlgorithm; got != x509.ECDSAWithSHA256 {
		t.Errorf("signature algorithm = %v, want ECDSA-SHA256", got)
	}
	if cert.IsCA {
		t.Error("leaf certificate must not be a CA")
	}
	if cert.KeyUsage&x509.KeyUsageCertSign != 0 {
		t.Error("leaf must not be able to sign certificates")
	}
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("leaf needs digitalSignature for TLS")
	}
	if cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Error("leaf needs keyEncipherment for TLS")
	}
	// Missing serverAuth is why hand-rolled certificates get rejected by
	// strict clients even when the rest is right.
	if len(cert.ExtKeyUsage) == 0 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want serverAuth first", cert.ExtKeyUsage)
	}
}

// TestGenerateSelfSignedSANs is the load-bearing test: modern browsers and Go's
// crypto/tls ignore the Common Name and read only the SANs. A certificate with
// CN=localhost and no SAN is rejected by everything modern.
func TestGenerateSelfSignedSANs(t *testing.T) {
	loopbacks := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	for _, tc := range []struct {
		domain  string
		wantDNS []string
		wantIP  []net.IP
	}{
		{"localhost", []string{"localhost"}, loopbacks},
		// RFC 6761 reserves the whole *.localhost zone for loopback, so these
		// SANs are true, not a convenience.
		{"api.localhost", []string{"api.localhost"}, loopbacks},
		// A private name resolved through /etc/hosts gets no IP SAN: claiming
		// 127.0.0.1 for it would be an assertion easydrop cannot verify.
		{"api.local.test", []string{"api.local.test"}, nil},
		{"example.com", []string{"example.com"}, nil},
		{"10.1.2.3", nil, []net.IP{net.ParseIP("10.1.2.3")}},
		{"::1", nil, []net.IP{net.IPv6loopback}},
	} {
		cert := parseLeaf(t, mustGenerateCert(t, tc.domain, time.Hour))

		if len(cert.DNSNames) != len(tc.wantDNS) {
			t.Errorf("%s: DNSNames = %v, want %v", tc.domain, cert.DNSNames, tc.wantDNS)
			continue
		}
		for i, want := range tc.wantDNS {
			if cert.DNSNames[i] != want {
				t.Errorf("%s: DNSNames[%d] = %q, want %q", tc.domain, i, cert.DNSNames[i], want)
			}
		}
		if len(cert.IPAddresses) != len(tc.wantIP) {
			t.Errorf("%s: IPAddresses = %v, want %v", tc.domain, cert.IPAddresses, tc.wantIP)
			continue
		}
		for i, want := range tc.wantIP {
			if !cert.IPAddresses[i].Equal(want) {
				t.Errorf("%s: IPAddresses[%d] = %v, want %v", tc.domain, i, cert.IPAddresses[i], want)
			}
		}
		// CN is kept only for legacy clients and must never be the sole identity.
		if cert.Subject.CommonName != tc.domain {
			t.Errorf("%s: CommonName = %q, want %q", tc.domain, cert.Subject.CommonName, tc.domain)
		}
	}
}

func TestGenerateSelfSignedRejectsUncertifiableDomains(t *testing.T) {
	for _, domain := range []string{"*.example.com", "", "  ", "has space.com", "a;rm -rf /.com"} {
		if _, _, _, err := generateSelfSigned(certOptions{domain: domain, validFor: time.Hour}); err == nil {
			t.Errorf("domain %q must be refused", domain)
		}
	}
}

func TestGenerateSelfSignedValidity(t *testing.T) {
	cert := parseLeaf(t, mustGenerateCert(t, "localhost", 90*24*time.Hour))
	window := cert.NotAfter.Sub(cert.NotBefore)
	if window < 90*24*time.Hour || window > 91*24*time.Hour {
		t.Errorf("validity window = %v, want ~90 days (NotBefore is backdated 1h for skew)", window)
	}
	// Backdated start: a certificate that begins "now" fails on a host whose
	// clock is a minute behind.
	if !cert.NotBefore.Before(time.Now()) {
		t.Errorf("NotBefore = %v, want backdated for clock skew", cert.NotBefore)
	}
}

// --- reuse / rotation ----------------------------------------------------

func TestUsableKeepsValidMatchingCert(t *testing.T) {
	cert := parseLeaf(t, mustGenerateCert(t, "localhost", certValidity))
	if !usable(cert, "localhost", time.Now()) {
		t.Error("a fresh, matching certificate must be kept")
	}
}

func TestUsableRotatesExpiringCert(t *testing.T) {
	// The case that matters in practice: a certificate that is replaced on every
	// deploy would force the user to re-trust it in the browser every time.
	expiring := parseLeaf(t, mustGenerateCert(t, "localhost", 5*24*time.Hour))
	if usable(expiring, "localhost", time.Now()) {
		t.Error("a certificate inside the renewal window must be rotated")
	}
	// A long-lived certificate evaluated inside its own remaining life is kept.
	long := parseLeaf(t, mustGenerateCert(t, "localhost", certValidity))
	if !usable(long, "localhost", time.Now()) {
		t.Error("a full-lifetime certificate must be kept")
	}
}

func TestUsableRotatesWrongName(t *testing.T) {
	cert := parseLeaf(t, mustGenerateCert(t, "other.local", certValidity))
	if usable(cert, "localhost", time.Now()) {
		t.Error("a certificate naming another host must not be reused")
	}
}

func TestUsableIgnoresCommonNameOnly(t *testing.T) {
	// A certificate whose identity lives only in CN passes every legacy check
	// and fails in the browser, so it must not be treated as usable.
	cert := &x509.Certificate{
		Subject:  pkix.Name{CommonName: "localhost"},
		NotAfter: time.Now().Add(certValidity),
	}
	if usable(cert, "localhost", time.Now()) {
		t.Error("a CN-only certificate must be rotated")
	}
}

func TestUsableRejectsGarbage(t *testing.T) {
	if _, err := x509.ParseCertificate([]byte("not a certificate")); err == nil {
		t.Error("garbage must not parse as a certificate")
	}
	if len(pemBlocks(t, "not a pem file")) != 0 {
		t.Error("garbage must not decode to PEM blocks")
	}
	if len(pemBlocks(t, "")) != 0 {
		t.Error("empty input must not decode to PEM blocks")
	}
}

func TestCertPaths(t *testing.T) {
	certPath, keyPath, dir := certPaths("api.example.com")
	if certPath != "/etc/nginx/ssl/api.example.com.crt" {
		t.Errorf("cert path = %q", certPath)
	}
	if keyPath != "/etc/nginx/ssl/api.example.com.key" {
		t.Errorf("key path = %q", keyPath)
	}
	if dir != "/etc/nginx/ssl" {
		t.Errorf("dir = %q", dir)
	}
}

// --- host interaction ----------------------------------------------------

// catFake answers the read/inspect commands EnsureCert issues and returns a
// fixed certificate for `cat`.
type catFake struct {
	existing   string
	stdout     map[string]string
	noCertFile bool
	noVhost    bool
	uploads    map[string]string
	calls      []string
	script     map[string]error
	mvFail     bool
}

// scriptTLS teaches the fake the rest of the nginx apply chain, so one fake
// covers a full TLS Apply.
func (f *catFake) scriptTLS() {
	for _, c := range []string{
		"sudo mv '/tmp/easydrop/nginx/localhost' '/etc/nginx/sites-available/localhost'",
		"sudo ln -sf '/etc/nginx/sites-available/localhost' '/etc/nginx/sites-enabled/'",
		"sudo nginx -t",
		"sudo systemctl reload nginx",
	} {
		if f.script == nil {
			f.script = map[string]error{}
		}
		f.script[c] = nil
	}
}

func (f *catFake) ExecCommand(_ context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	if out, ok := f.stdout[cmd]; ok {
		return out, "", 0, nil
	}
	if f.script != nil {
		if err, ok := f.script[cmd]; ok {
			if err != nil {
				return "", "fake stderr", 1, err
			}
			return "", "", 0, nil
		}
	}
	switch {
	case strings.HasPrefix(cmd, "sudo test -e"):
		if f.noVhost {
			return "", "exit 1", 1, fmt.Errorf("fake: exit 1")
		}
		return "", "", 0, nil
	case strings.HasPrefix(cmd, "sudo mkdir"):
		return "", "", 0, nil
	case strings.HasPrefix(cmd, "sudo test -f"):
		if f.noCertFile {
			return "", "exit 1", 1, fmt.Errorf("fake: exit 1")
		}
		return "", "", 0, nil
	case strings.HasPrefix(cmd, "sudo cat"):
		return f.existing, "", 0, nil
	case strings.HasPrefix(cmd, "sudo chmod"):
		return "", "", 0, nil
	case strings.HasPrefix(cmd, "sudo mv"):
		if f.mvFail {
			return "", "permission denied", 1, fmt.Errorf("fake: permission denied")
		}
		return "", "", 0, nil
	}
	return "", "", -1, fmt.Errorf("catFake: unexpected command %q", cmd)
}

func (f *catFake) UploadFile(_ context.Context, srcPath, destPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	if f.uploads == nil {
		f.uploads = map[string]string{}
	}
	f.uploads[destPath] = string(data)
	return nil
}

func (f *catFake) Close() error { return nil }

func (f *catFake) ran(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestEnsureCertGeneratesWhenMissing(t *testing.T) {
	f := &catFake{noCertFile: true}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}

	res, err := c.EnsureCert(t.Context(), "localhost")
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}
	if !res.Created {
		t.Error("certificate must be reported as created")
	}
	// Uploads land on the staging path (SFTP cannot write /etc/nginx/ssl) and
	// are moved into place by `sudo mv` – asserted below.
	for _, suffix := range []string{"localhost.crt", "localhost.key"} {
		staged := "/tmp/easydrop/tls/" + suffix
		if _, ok := f.uploads[staged]; !ok {
			t.Errorf("%s must be uploaded to %s, got %v", suffix, staged, f.uploads)
		}
	}
	if !f.ran("sudo mv '/tmp/easydrop/tls/localhost.crt' " + q(res.CertPath)) {
		t.Errorf("certificate must be moved into %s, ran: %v", res.CertPath, f.calls)
	}
	if !f.ran("sudo mv '/tmp/easydrop/tls/localhost.key' " + q(res.KeyPath)) {
		t.Errorf("private key must be moved into %s, ran: %v", res.KeyPath, f.calls)
	}
	// A world-readable private key is a real defect, not a style point.
	if !f.ran("chmod 0600 " + q(res.KeyPath)) {
		t.Errorf("private key must be chmod 0600, ran: %v", f.calls)
	}
	if !f.ran("chmod 0644 " + q(res.CertPath)) {
		t.Errorf("certificate may be world-readable, ran: %v", f.calls)
	}
	// Privileged paths are never written directly (SFTP cannot); stage + move.
	if !f.ran("sudo mv ") {
		t.Errorf("install must stage and sudo mv, ran: %v", f.calls)
	}
	if res.NotAfter.IsZero() {
		t.Error("expiry must be reported so the message can name it")
	}
}

func TestEnsureCertReusesExisting(t *testing.T) {
	f := &catFake{existing: mustGenerateCert(t, "localhost", certValidity)}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}

	res, err := c.EnsureCert(t.Context(), "localhost")
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}
	if res.Created {
		t.Error("a valid matching certificate must be reused, not regenerated")
	}
	if len(f.uploads) != 0 {
		t.Errorf("nothing may be uploaded on reuse, got %v", f.uploads)
	}
	if f.ran("sudo mv") {
		t.Errorf("no install step on reuse, ran: %v", f.calls)
	}
}

func TestEnsureCertRotatesExpiring(t *testing.T) {
	f := &catFake{existing: mustGenerateCert(t, "localhost", 24*time.Hour)}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}

	res, err := c.EnsureCert(t.Context(), "localhost")
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}
	if !res.Created {
		t.Error("an expiring certificate must be rotated")
	}
	if len(f.uploads) == 0 {
		t.Error("the replacement must be uploaded")
	}
}

func TestEnsureCertRotatesGarbage(t *testing.T) {
	// An unreadable certificate file must degrade to regeneration rather than
	// blocking the deploy: nginx would fail to start with it anyway.
	f := &catFake{existing: "corrupted garbage"}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}

	res, err := c.EnsureCert(t.Context(), "localhost")
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}
	if !res.Created {
		t.Error("a corrupt certificate must be replaced")
	}
}

func TestEnsureCertRejectsBadDomainBeforeTouchingHost(t *testing.T) {
	for _, domain := range []string{"*.example.com", "", "a b.com", "evil.com; rm -rf /"} {
		f := &catFake{}
		c := NewSelfSignedCertifier(f)
		c.Out = &bytes.Buffer{}
		if _, err := c.EnsureCert(t.Context(), domain); err == nil {
			t.Errorf("domain %q must be refused", domain)
		}
		if len(f.calls) != 0 || len(f.uploads) != 0 {
			t.Errorf("no host command may run for %q: %v", domain, f.calls)
		}
	}
}

func TestEnsureCertReportsInstallFailure(t *testing.T) {
	f := &catFake{noCertFile: true, mvFail: true}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}
	if _, err := c.EnsureCert(t.Context(), "localhost"); err == nil {
		t.Fatal("a failed install must be reported, not swallowed")
	}
}

// TestGeneratedCertVerifiesInGoTLS is the end-to-end crypto check: the
// certificate and key must actually load into a tls.Certificate and pass
// hostname verification. Every other assertion inspects fields; this one asks
// the same question a real client asks.
func TestGeneratedCertVerifiesInGoTLS(t *testing.T) {
	certPEM, keyPEM, _, err := generateSelfSigned(certOptions{domain: "localhost", validFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair (what nginx and every TLS client do): %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	for _, name := range []string{"localhost", "127.0.0.1", "::1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{
			DNSName: name,
			Roots:   pool,
			// Self-signed: it is its own root. Without this the verification
			// fails on the signature chain, which is exactly what a browser does
			// before the user chooses to trust it.
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			t.Errorf("certificate must verify for %q: %v", name, err)
		}
	}
}

// --- purge (M19) --------------------------------------------------------

func TestRemoveCertDeletesBothFiles(t *testing.T) {
	f := &catFake{}
	f.script = map[string]error{
		"sudo test -f '/etc/nginx/ssl/localhost.crt'": nil,
		"sudo test -f '/etc/nginx/ssl/localhost.key'": nil,
		"sudo rm -f '/etc/nginx/ssl/localhost.crt'":   nil,
		"sudo rm -f '/etc/nginx/ssl/localhost.key'":   nil,
	}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}

	removed, err := c.RemoveCert(t.Context(), "localhost")
	if err != nil {
		t.Fatalf("RemoveCert: %v", err)
	}
	if !removed {
		t.Error("removal must be reported")
	}
	for _, path := range []string{
		"/etc/nginx/ssl/localhost.crt",
		"/etc/nginx/ssl/localhost.key",
	} {
		if !f.ran("sudo rm -f '" + path + "'") {
			t.Errorf("%s must be deleted, ran: %v", path, f.calls)
		}
	}
}

func TestRemoveCertIsIdempotent(t *testing.T) {
	f := &catFake{}
	f.noCertFile = true
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}

	removed, err := c.RemoveCert(t.Context(), "localhost")
	if err != nil {
		t.Fatalf("a missing certificate is not an error: %v", err)
	}
	if removed {
		t.Error("nothing was deleted, and the result must not claim otherwise")
	}
	if f.ran("rm -f") {
		t.Errorf("no removal command may run when there is nothing to remove: %v", f.calls)
	}
}

func TestRemoveCertRejectsBadDomain(t *testing.T) {
	f := &catFake{}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}
	for _, domain := range []string{"*.example.com", "", "a;rm -rf /.crt"} {
		if _, err := c.RemoveCert(t.Context(), domain); err == nil {
			t.Errorf("domain %q must be refused", domain)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("no host command on invalid input: %v", f.calls)
	}
}

// --- Describe (status) --------------------------------------------------

func TestDescribeReportsExpiry(t *testing.T) {
	f := &catFake{existing: mustGenerateCert(t, "localhost", certValidity)}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}
	got, err := c.Describe(t.Context(), "localhost")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(got, "self-signed") || !strings.Contains(got, "expires") {
		t.Errorf("status must name the mode and the expiry, got %q", got)
	}
	if strings.Contains(got, "EXPIRING") {
		t.Errorf("a fresh certificate must not be reported as expiring, got %q", got)
	}
}

func TestDescribeFlagsExpiringCertificate(t *testing.T) {
	// The whole reason status reads the file: an expiring certificate is the one
	// thing the user can act on before it breaks.
	f := &catFake{existing: mustGenerateCert(t, "localhost", 24*time.Hour)}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}
	got, _ := c.Describe(t.Context(), "localhost")
	if !strings.Contains(got, "EXPIRING") {
		t.Errorf("a certificate inside the renewal window must be flagged, got %q", got)
	}
}

func TestDescribeReportsMissingCertificate(t *testing.T) {
	// "no answer" and "certificate missing" must not look alike.
	f := &catFake{noCertFile: true}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}
	got, _ := c.Describe(t.Context(), "localhost")
	if !strings.Contains(got, "missing") {
		t.Errorf("a missing certificate must be named, got %q", got)
	}
}

func TestDescribeReportsUnreadableCertificate(t *testing.T) {
	f := &catFake{existing: "garbage"}
	c := NewSelfSignedCertifier(f)
	c.Out = &bytes.Buffer{}
	got, _ := c.Describe(t.Context(), "localhost")
	if !strings.Contains(got, "unreadable") {
		t.Errorf("an unparseable certificate must be named, got %q", got)
	}
}
