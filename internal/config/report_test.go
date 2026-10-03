package config

import (
	"strings"
	"testing"
)

func reportFor(t *testing.T, files map[string]string, opts ScaffoldOptions) string {
	t.Helper()
	res, err := ScaffoldWith(mkScaffoldDir(t, files), opts)
	if err != nil {
		t.Fatalf("ScaffoldWith: %v", err)
	}
	return res.Report()
}

func TestReportDetectedPort(t *testing.T) {
	got := reportFor(t, map[string]string{
		"Dockerfile": "FROM node:22\nEXPOSE 3000\n",
		"go.mod":     "module example.com/x\n",
	}, ScaffoldOptions{})

	for _, want := range []string{"wrote easydrop.toml", "port    3000", PortSourceExpose, "stack   go", "driver  single"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// The whole point of a detected port: no nagging.
	for _, unwanted := range []string{"ACTION REQUIRED", "NOT SET"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("report should not contain %q when a port was found:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "host = \"localhost\"") {
		t.Errorf("report must keep the [server] hint:\n%s", got)
	}
}

// TestReportMissingPortIsAnInstruction pins the contract that M15 exists for:
// the block an agent receives must contain the METHOD, not only the
// prohibition, and must name both supported ways to supply a port.
func TestReportMissingPortIsAnInstruction(t *testing.T) {
	got := reportFor(t, map[string]string{"README.md": "no port here\n"}, ScaffoldOptions{})

	required := []string{
		"NOT SET",
		"ACTION REQUIRED",
		"[app].port",
		// the method: where to look
		"app.listen",
		"ListenAndServe",
		"uvicorn.run",
		"--port",
		"PORT=",
		// the obligation: it must edit the file itself
		"edit the file yourself",
		// when it may ask the human
		"Ask the user only if the code leaves the port genuinely ambiguous",
		// both escape hatches
		"easydrop init --port",
		`init_project { "port":`,
		// the cost of getting it wrong
		"healthcheck",
		"502",
	}
	for _, want := range required {
		if !strings.Contains(got, want) {
			t.Errorf("missing-port report missing %q:\n%s", want, got)
		}
	}
}

// TestReportOverrideStatesItsSource keeps the override honest: the user (or
// agent) must see that detection was bypassed, not that easydrop guessed it.
func TestReportOverrideStatesItsSource(t *testing.T) {
	got := reportFor(t, map[string]string{
		"Dockerfile": "FROM node:22\nEXPOSE 3000\n",
	}, ScaffoldOptions{Port: 8080})

	if !strings.Contains(got, "port    8080 ("+PortSourceOverride+")") {
		t.Errorf("override must be reported with its source:\n%s", got)
	}
	if strings.Contains(got, "NOT SET") || strings.Contains(got, "ACTION REQUIRED") {
		t.Errorf("an explicit port needs no remediation:\n%s", got)
	}
}

func TestScaffoldOptionsOverrideBeatsDetection(t *testing.T) {
	dir := mkScaffoldDir(t, map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 3000\n"})

	detected, err := ScaffoldWith(dir, ScaffoldOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if detected.App.Port != 3000 || detected.PortSource != PortSourceExpose {
		t.Errorf("without an override the detection must stand, got %d (%s)", detected.App.Port, detected.PortSource)
	}

	// host_port defaults to the overridden port rather than the detected one.
	res, err := ScaffoldWith(dir, ScaffoldOptions{Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if res.App.Port != 8080 || res.PortSource != PortSourceOverride {
		t.Errorf("override ignored: port=%d (%s)", res.App.Port, res.PortSource)
	}
	if res.App.HostPort != 8080 {
		t.Errorf("HostPort = %d, want it to follow the overridden port", res.App.HostPort)
	}
	if res.PortMissing() {
		t.Errorf("an explicit port must not be reported as missing")
	}
}

func TestScaffoldOptionsZeroMeansDetect(t *testing.T) {
	dir := mkScaffoldDir(t, map[string]string{"Dockerfile": "FROM python:3.12\nEXPOSE 8000\n"})
	res, err := ScaffoldWith(dir, ScaffoldOptions{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.App.Port != 8000 || res.PortSource != PortSourceExpose {
		t.Errorf("Port 0 must mean \"detect\", got %d (%s)", res.App.Port, res.PortSource)
	}
}

func TestScaffoldOptionsRejectsInvalidPort(t *testing.T) {
	dir := mkScaffoldDir(t, map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 3000\n"})
	for _, port := range []int{-1, 65536, 99999} {
		if _, err := ScaffoldWith(dir, ScaffoldOptions{Port: port}); err == nil {
			t.Errorf("ScaffoldWith(port=%d) must fail", port)
		}
	}
}

func TestReportComposeNote(t *testing.T) {
	got := reportFor(t, map[string]string{
		"docker-compose.yml": "services:\n  web:\n    ports: [\"8080:80\"]\n",
	}, ScaffoldOptions{})

	if !strings.Contains(got, "driver  compose") {
		t.Errorf("driver must be compose:\n%s", got)
	}
	// For compose, app.port is the host-published port – worth repeating.
	if !strings.Contains(got, "publishes on the host") {
		t.Errorf("compose report must explain the host-port meaning:\n%s", got)
	}
	// ...but the remediation block must not appear, since the port was found.
	if strings.Contains(got, "ACTION REQUIRED") {
		t.Errorf("compose with a detected port needs no remediation:\n%s", got)
	}
}

func TestReportNilSafe(t *testing.T) {
	var r *ScaffoldResult
	if got := r.Report(); got == "" {
		t.Errorf("Report on nil must not return empty output")
	}
}
