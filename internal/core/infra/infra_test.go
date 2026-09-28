package infra

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
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
