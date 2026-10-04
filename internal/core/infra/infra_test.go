package infra

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scriptFake scripts ExecCommand by exact command and captures uploads.
type scriptFake struct {
	script  map[string]error
	calls   []string
	uploads map[string]string
}

func (f *scriptFake) ExecCommand(_ context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	if err, ok := f.script[cmd]; ok {
		if err != nil {
			return "", "fake stderr", 1, err
		}
		return "", "", 0, nil
	}
	return "", "", -1, fmt.Errorf("fake: unexpected command %q", cmd)
}

func (f *scriptFake) UploadFile(_ context.Context, srcPath, destPath string) error {
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

func (f *scriptFake) Close() error { return nil }

func (f *scriptFake) ran(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func nginxHappyFake() *scriptFake {
	return &scriptFake{script: map[string]error{
		"sudo mv '/tmp/easydrop/nginx/example.com' '/etc/nginx/sites-available/example.com'": nil,
		"sudo ln -sf '/etc/nginx/sites-available/example.com' '/etc/nginx/sites-enabled/'":   nil,
		"sudo nginx -t":               nil,
		"sudo systemctl reload nginx": nil,
	}}
}

func TestNginxApplyHappyPath(t *testing.T) {
	f := nginxHappyFake()
	m := NewNginxManager(f)
	m.Out = &bytes.Buffer{}
	if err := m.Apply(context.Background(), "example.com", 8080); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	staged, ok := f.uploads["/tmp/easydrop/nginx/example.com"]
	if !ok {
		t.Fatalf("expected staged upload, got %v", f.uploads)
	}
	for _, want := range []string{"server_name example.com;", "proxy_pass http://127.0.0.1:8080;"} {
		if !strings.Contains(staged, want) {
			t.Errorf("rendered config must contain %q, got:\n%s", want, staged)
		}
	}
	if !f.ran("sudo systemctl reload nginx") {
		t.Errorf("reload must run last, ran: %v", f.calls)
	}
}

func TestNginxApplyRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		domain string
		port   int
	}{
		{"evil.com; rm -rf /", 8080},
		{"a b.com", 80},
		{"", 80},
		{"example.com", 0},
		{"example.com", 99999},
	} {
		f := nginxHappyFake()
		m := NewNginxManager(f)
		m.Out = &bytes.Buffer{}
		if err := m.Apply(context.Background(), tc.domain, tc.port); err == nil {
			t.Errorf("Apply(%q, %d) expected error, got nil", tc.domain, tc.port)
		}
		if len(f.calls) != 0 || len(f.uploads) != 0 {
			t.Errorf("no host touch on invalid input (%q, %d): %v", tc.domain, tc.port, f.calls)
		}
	}
}

func TestNginxApplyTestFailureSkipsReload(t *testing.T) {
	f := nginxHappyFake()
	f.script["sudo nginx -t"] = fmt.Errorf("fake: emergies")
	m := NewNginxManager(f)
	m.Out = &bytes.Buffer{}
	if err := m.Apply(context.Background(), "example.com", 8080); err == nil {
		t.Fatalf("Apply() expected nginx -t error, got nil")
	}
	if f.ran("reload nginx") {
		t.Errorf("reload must NOT run after failed config test, ran: %v", f.calls)
	}
}

func TestNginxUpdateIngressSatisfiesDriver(t *testing.T) {
	f := nginxHappyFake()
	m := NewNginxManager(f)
	m.Out = &bytes.Buffer{}
	if err := m.UpdateIngress(context.Background(), "example.com", 8081); err != nil {
		t.Fatalf("UpdateIngress() unexpected error: %v", err)
	}
	if staged := f.uploads["/tmp/easydrop/nginx/example.com"]; !strings.Contains(staged, "proxy_pass http://127.0.0.1:8081;") {
		t.Errorf("port must propagate to rendered config, got:\n%s", staged)
	}
}

func TestCertbotSkipsLocalhost(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1"} {
		f := &scriptFake{script: map[string]error{}}
		m := NewCertbotManager(f, host)
		var logBuf bytes.Buffer
		m.Out = &logBuf
		if err := m.EnableSSL(context.Background(), "example.com", ""); err != nil {
			t.Fatalf("EnableSSL() on %s must succeed, got: %v", host, err)
		}
		if len(f.calls) != 0 {
			t.Errorf("no host touch on localhost, ran: %v", f.calls)
		}
	}
}

func TestCertbotWithEmail(t *testing.T) {
	f := &scriptFake{script: map[string]error{
		"sudo certbot --nginx -d 'example.com' --non-interactive --agree-tos --email 'admin@example.com'": nil,
	}}
	m := NewCertbotManager(f, "203.0.113.10")
	m.Out = &bytes.Buffer{}
	if err := m.EnableSSL(context.Background(), "example.com", "admin@example.com"); err != nil {
		t.Fatalf("EnableSSL() unexpected error: %v", err)
	}
}

func TestCertbotWithoutEmail(t *testing.T) {
	f := &scriptFake{script: map[string]error{
		"sudo certbot --nginx -d 'example.com' --non-interactive --agree-tos --register-unsafely-without-email": nil,
	}}
	m := NewCertbotManager(f, "203.0.113.10")
	m.Out = &bytes.Buffer{}
	if err := m.EnableSSL(context.Background(), "example.com", ""); err != nil {
		t.Fatalf("EnableSSL() unexpected error: %v", err)
	}
}

func TestCertbotInvalidDomain(t *testing.T) {
	f := &scriptFake{script: map[string]error{}}
	m := NewCertbotManager(f, "203.0.113.10")
	m.Out = &bytes.Buffer{}
	if err := m.EnableSSL(context.Background(), "evil; x", ""); err == nil {
		t.Errorf("EnableSSL() expected validation error, got nil")
	}
	if len(f.calls) != 0 {
		t.Errorf("no host touch on invalid domain, ran: %v", f.calls)
	}
}

func TestValidateDomain(t *testing.T) {
	for _, good := range []string{"example.com", "my-proj.io", "a.b.c.de", "*.example.com"} {
		if err := validateDomain(good); err != nil {
			t.Errorf("validateDomain(%q) unexpected error: %v", good, err)
		}
	}
	for _, bad := range []string{"", "a b", "x;rm", "example.com/", "-"} {
		if err := validateDomain(bad); err == nil {
			t.Errorf("validateDomain(%q) expected error, got nil", bad)
		}
	}
}

// --- self-signed TLS vhost (M18) ----------------------------------------

// tlsFake answers the certificate commands and the nginx apply chain.
func tlsFake() *catFake {
	return &catFake{noCertFile: true, calls: nil}
}

func TestNginxTLSServesBothPorts(t *testing.T) {
	f := tlsFake()
	f.scriptTLS()
	m := NewNginxManagerTLS(f)
	m.Out = &bytes.Buffer{}
	if err := m.Apply(context.Background(), "localhost", 8080); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	var staged string
	for path, body := range f.uploads {
		if strings.HasSuffix(path, "/nginx/localhost") {
			staged = body
		}
	}
	if staged == "" {
		t.Fatalf("vhost must be uploaded, got %v", f.uploads)
	}
	for _, want := range []string{
		"listen 80;",
		"listen 443 ssl;",
		"ssl_certificate /etc/nginx/ssl/localhost.crt;",
		"ssl_certificate_key /etc/nginx/ssl/localhost.key;",
		"server_name localhost;",
		"proxy_pass http://127.0.0.1:8080;",
	} {
		if !strings.Contains(staged, want) {
			t.Errorf("rendered vhost must contain %q, got:\n%s", want, staged)
		}
	}
	// Port 80 must keep proxying, not redirect: in self-signed mode a 301 sends
	// the developer from a working http:// URL to a certificate warning.
	if strings.Contains(staged, "return 301") {
		t.Errorf("self-signed vhost must not redirect 80 -> 443, got:\n%s", staged)
	}
}

func TestNginxPlainTemplateNeverReferencesCertificate(t *testing.T) {
	// The Let's Encrypt invariant: easydrop writes no 443 block, certbot does.
	// A conditional smuggled into the shared template would break it silently.
	f := &scriptFake{script: map[string]error{
		"sudo mv '/tmp/easydrop/nginx/example.com' '/etc/nginx/sites-available/example.com'": nil,
		"sudo ln -sf '/etc/nginx/sites-available/example.com' '/etc/nginx/sites-enabled/'":   nil,
		"sudo nginx -t":               nil,
		"sudo systemctl reload nginx": nil,
	}}
	m := NewNginxManager(f)
	m.Out = &bytes.Buffer{}
	if err := m.Apply(context.Background(), "example.com", 8080); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	staged := f.uploads["/tmp/easydrop/nginx/example.com"]
	for _, forbidden := range []string{"443", "ssl_certificate", "ssl"} {
		if strings.Contains(staged, forbidden) {
			t.Errorf("plain vhost must not mention %q (certbot owns the TLS block):\n%s", forbidden, staged)
		}
	}
}

func TestNginxTLSProvisionsCertificateBeforeConfigTest(t *testing.T) {
	// `nginx -t` validates that ssl_certificate exists. Provisioning after it
	// would break the first TLS deploy on a host with no prior config.
	f := tlsFake()
	f.scriptTLS()
	m := NewNginxManagerTLS(f)
	m.Out = &bytes.Buffer{}
	if err := m.Apply(context.Background(), "localhost", 8080); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	nginxTest := indexOf(f.calls, "sudo nginx -t")
	mvCert := indexOf(f.calls, "/tmp/easydrop/tls/localhost.crt")
	if mvCert < 0 || nginxTest < 0 {
		t.Fatalf("expected both the install and the config test, ran: %v", f.calls)
	}
	if mvCert > nginxTest {
		t.Errorf("certificate must be installed before nginx -t, ran: %v", f.calls)
	}
}

func TestNginxTLSRejectsBadDomainBeforeCertProvisioning(t *testing.T) {
	f := tlsFake()
	f.scriptTLS()
	m := NewNginxManagerTLS(f)
	m.Out = &bytes.Buffer{}
	if err := m.Apply(context.Background(), "evil.com; rm -rf /", 8080); err == nil {
		t.Fatal("invalid domain must be rejected")
	}
	for _, c := range f.calls {
		if strings.Contains(c, "ssl") || strings.Contains(c, "mkdir -p /etc/nginx/ssl") {
			t.Errorf("no certificate work on invalid input, ran: %v", f.calls)
		}
	}
}

func TestNginxTLSReportsTrustInstructions(t *testing.T) {
	f := tlsFake()
	f.scriptTLS()
	m := NewNginxManagerTLS(f)
	var out bytes.Buffer
	m.Out = &out
	if err := m.Apply(context.Background(), "localhost", 8080); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	// easydrop must not install anything into the system trust store, so it has
	// to tell the user how to do it themselves.
	for _, want := range []string{"update-ca-certificates", "curl --cacert", "browser will warn"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must mention %q, got:\n%s", want, out.String())
		}
	}
}

// indexOf returns the position of the first command containing needle, or -1.
func indexOf(hay []string, needle string) int {
	for i, h := range hay {
		if strings.Contains(h, needle) {
			return i
		}
	}
	return -1
}

// TestNginxTLSTemplateIsAcceptedByNginx renders the real template with a real
// generated certificate and hands the result to the real nginx binary. Every
// other test here inspects strings; this one asks nginx whether the file it
// would actually be given is valid – the class of mistake (a typo in a
// directive, a certificate path nginx cannot read) that only shows up as a
// 502 after a deploy.
//
// Runs unprivileged with `nginx -t -p <tmpdir>`, so no systemd and no root.
func TestNginxTLSTemplateIsAcceptedByNginx(t *testing.T) {
	nginxBin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx not installed; skipping the real config syntax check")
	}
	dir := t.TempDir()

	certPEM, keyPEM, _, err := generateSelfSigned(certOptions{domain: "localhost", validFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"localhost.crt": certPEM, "localhost.key": keyPEM} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	m := &NginxManager{selfSigned: true}
	rendered, err := m.render("localhost", 8080, CertResult{
		CertPath: filepath.Join(dir, "localhost.crt"),
		KeyPath:  filepath.Join(dir, "localhost.key"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// `nginx -t` binds the configured ports, so :80/:443 are unreachable without
	// root. Only the listen lines are rewritten; the certificate directives –
	// the thing this test exists for – are left exactly as rendered.
	checkable := strings.ReplaceAll(rendered, "listen 80;", "listen 18080;")
	checkable = strings.ReplaceAll(checkable, "listen 443 ssl;", "listen 18443 ssl;")

	conf := filepath.Join(dir, "nginx.conf")
	body := "worker_processes 1;\nerror_log " + filepath.Join(dir, "error.log") + " warn;\npid " +
		filepath.Join(dir, "nginx.pid") + ";\nevents { worker_connections 64; }\nhttp {\n" +
		"access_log off;\n" + tempPaths(dir) + checkable + "\n}\n"
	if err := os.WriteFile(conf, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(nginxBin, "-t", "-c", conf, "-p", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("nginx rejected the rendered vhost: %v\n%s\n---\n%s", err, out, checkable)
	}
}

func tempPaths(dir string) string {
	var sb strings.Builder
	for _, p := range []string{"client_body", "proxy", "fastcgi", "uwsgi", "scgi"} {
		fmt.Fprintf(&sb, "%s_temp_path %s;\n", p, filepath.Join(dir, p))
	}
	return sb.String()
}
