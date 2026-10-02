package config

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sources reported by detectPort (M14) so `init` can explain every guess
// instead of writing a silent number into the file.
const (
	PortSourceExpose        = "Dockerfile EXPOSE"
	PortSourceDockerRuntime = "Dockerfile final stage (web server)"
	PortSourceDockerCmd     = "Dockerfile CMD/ENV"
	PortSourceComposePort   = "compose ports:"
	PortSourceComposeExp    = "compose expose:"
	PortSourceSourceScan    = "source scan"
	// PortSourceUnknown means nothing in the project stated the port. No value
	// is written to the config in that case (OD-04): a guessed 8080 builds fine
	// and then breaks the deploy, which is far more expensive to debug.
	PortSourceUnknown = "not detected"
)

// composeFileNames is the lookup order for a compose file. Driver detection and
// the `driver.compose_file` default MUST agree: the file `init` found is the
// file the drivers will run.
var composeFileNames = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

// findComposeFile returns the compose file present in dir, if any.
func findComposeFile(dir string) (string, bool) {
	for _, name := range composeFileNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name, true
		}
	}
	return "", false
}

// detectPort resolves app.port and the source it came from (M14).
//
// Precedence, and why it is not one flat list:
//
//   - compose driver: the compose mapping outranks EXPOSE, because for
//     compose/swarm `app.port` is the port published ON THE HOST (that is what
//     the Nginx vhost proxies to – see OD-01 and examples/13), while EXPOSE
//     names the container-side port.
//   - single driver: EXPOSE outranks everything else, then package.json, then
//     a bounded source scan, then the 8080 default. There is no compose file in
//     this case – driver detection already chose `compose` if one exists.
func detectPort(dir, driver, composeFile string) (int, string) {
	if driver == "compose" {
		if port, source, ok := detectComposePort(dir, composeFile); ok {
			return port, source
		}
	}
	// Everything the image itself declares is ground truth: it describes the
	// container that will actually run, while a framework convention only
	// describes the ecosystem. Hence all four Dockerfile signals outrank
	// package.json and the source scan.
	if port, ok := detectDockerExpose(dir); ok {
		return port, PortSourceExpose
	}
	// A static-serve final stage (nginx) ignores the language conventions below,
	// so it has to be asked before them.
	if port, ok := detectDockerRuntimePort(dir); ok {
		return port, PortSourceDockerRuntime
	}
	// `CMD ["next", "start", "-p", "4000"]` or `ENV PORT=8080` pins the port
	// harder than any convention can: the image runs this command.
	if port, ok := detectDockerCmdPort(dir); ok {
		return port, PortSourceDockerCmd
	}
	// A port written literally in the project – an npm script, or a literal in
	// the source – is a fact, so it outranks the convention tables below. Those
	// only run when nothing in the project states the port outright.
	if port, source, ok := detectNodeScriptPort(dir); ok {
		return port, source
	}
	if port, ok := detectSourcePort(dir); ok {
		return port, PortSourceSourceScan
	}
	// Conventions: ecosystem defaults, weakest evidence.
	if port, source, ok := detectNodeFrameworkPort(dir); ok {
		return port, source
	}
	if port, source, ok := detectPythonPort(dir); ok {
		return port, source
	}
	// Nothing stated the port. Return 0 and let the caller leave the key out:
	// a fabricated default is worse than a missing one (OD-04).
	return 0, PortSourceUnknown
}

// dockerPortPattern matches a port the image declares in its CMD/ENTRYPOINT/ENV
// lines: an explicit flag, or a PORT= assignment. Same shapes as the general
// source scan, applied to one file for one reason – precedence.
var dockerPortPattern = regexp.MustCompile(`(?i)(?:PORT\s*=\s*(\d{2,5})\b)|(?:(?:--port|-p)["'=\s,]{0,5}(\d{2,5})\b)`)

// detectDockerCmdPort reads a port out of the Dockerfile's CMD, ENTRYPOINT or
// ENV lines. It is consulted after EXPOSE and the final-stage check, and before
// the language conventions: a Dockerfile that says `-p 4000` is telling you the
// container listens on 4000, and no amount of `next`→3000 knowledge overrides
// that.
func detectDockerCmdPort(dir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		return 0, false
	}
	var found int
	for _, line := range strings.Split(string(data), "\n") {
		upper := strings.ToUpper(strings.TrimSpace(line))
		if !strings.HasPrefix(upper, "CMD") && !strings.HasPrefix(upper, "ENTRYPOINT") && !strings.HasPrefix(upper, "ENV") {
			continue
		}
		m := dockerPortPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, group := range m[1:] {
			if port := toPort(group); port > 0 {
				found = port
			}
		}
	}
	return found, found > 0
}

// exposePattern matches an EXPOSE line, tolerant of case and tabs.
var exposePattern = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d+)`)

// fromPattern matches a FROM line, capturing the image reference.
var fromPattern = regexp.MustCompile(`(?im)^\s*FROM\s+(\S+)`)

// staticServerPorts are containers whose only job is to serve HTTP on a
// well-known port. A Vite/React/Vue build shipped as static files lands in
// `FROM nginx:alpine` far more often than in a Node runtime, so the framework
// convention (5173) would be wrong there – while the web server's port is
// right, whether it serves files or proxies to an upstream in the same image.
var staticServerPorts = []struct {
	image string
	port  int
}{
	{"nginx", 80},
	{"httpd", 80},
	{"apache", 80},
	{"caddy", 80},
}

// detectDockerRuntimePort infers the port from the Dockerfile's final stage when
// that stage is a plain web server with no EXPOSE. It is consulted after EXPOSE
// (explicit beats inferred) and before the language conventions, because a
// static-serve image ignores those entirely.
func detectDockerRuntimePort(dir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		return 0, false
	}
	matches := fromPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		return 0, false
	}
	final := matches[len(matches)-1][1]
	// Drop an image tag or digest: "nginx:alpine", "nginx@sha256:…".
	image := final
	if i := strings.IndexAny(image, ":@"); i > 0 {
		image = image[:i]
	}
	image = strings.ToLower(image)
	for _, srv := range staticServerPorts {
		if image == srv.image || image == srv.image+"-alpine" {
			return srv.port, true
		}
	}
	return 0, false
}

// detectDockerExpose returns the LAST EXPOSE in the Dockerfile. In a
// multi-stage build the final stage becomes the image, so its EXPOSE is the one
// that describes what the container actually listens on.
func detectDockerExpose(dir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		return 0, false
	}
	found := 0
	for _, m := range exposePattern.FindAllStringSubmatch(string(data), -1) {
		if port, err := strconv.Atoi(m[1]); err == nil && validPort(port) {
			found = port
		}
	}
	return found, found > 0
}

// composeModel is the only part of the compose schema we model. `ports:` and
// `expose:` are the sole keys that carry a port, so parsing the whole document
// with its interpolation, anchors and extension fields would be a lot of
// surface for no gain.
type composeModel struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Ports  []any `yaml:"ports"`
	Expose []any `yaml:"expose"`
}

// preferredServices are the names a human would call "the application". The
// remaining services follow in alphabetical order, so detection is
// deterministic rather than dependent on Go's map iteration order.
var preferredServices = []string{
	"web", "app", "api", "frontend", "server", "backend", "main", "www", "public", "http",
}

func orderedServices(services map[string]composeService) []string {
	ordered := make([]string, 0, len(services))
	taken := make(map[string]bool, len(services))
	for _, name := range preferredServices {
		if _, ok := services[name]; ok && !taken[name] {
			ordered = append(ordered, name)
			taken[name] = true
		}
	}
	rest := make([]string, 0, len(services))
	for name := range services {
		if !taken[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

// detectComposePort returns the host-published port of the first service that
// publishes one, then falls back to a container port from `expose:`.
//
// The host side is the right answer for compose/swarm: EasyDrop publishes
// nothing itself, the compose file does, and ingress points at the host port
// (deploy.go wires `UpdateIngress(ctx, domain, cfg.App.Port)`).
func detectComposePort(dir, composeFile string) (int, string, bool) {
	if composeFile == "" {
		return 0, "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, composeFile))
	if err != nil {
		return 0, "", false
	}
	var model composeModel
	if err := yaml.Unmarshal(data, &model); err != nil {
		return 0, "", false
	}
	order := orderedServices(model.Services)
	for _, name := range order {
		for _, entry := range model.Services[name].Ports {
			if port := composeHostPort(entry); port > 0 {
				return port, PortSourceComposePort + " " + composeFile + " (" + name + ")", true
			}
		}
	}
	for _, name := range order {
		for _, entry := range model.Services[name].Expose {
			if port := composeContainerPort(entry); port > 0 {
				return port, PortSourceComposeExp + " " + composeFile + " (" + name + ")", true
			}
		}
	}
	return 0, "", false
}

// composeHostPort extracts the host-published port from one `ports:` entry.
// It accepts the short string form ("8080:80", "127.0.0.1:8080:80", "8080/udp")
// and the long mapping form ({target: 80, published: 8080}). An entry that pins
// only a container port leaves the host port ephemeral, so it yields 0.
func composeHostPort(entry any) int {
	switch e := entry.(type) {
	case int:
		// `ports: - 3000` publishes on a random host port.
		return 0
	case string:
		if i := strings.Index(e, "/"); i >= 0 {
			e = e[:i]
		}
		parts := strings.Split(e, ":")
		if len(parts) < 2 {
			return 0
		}
		// host is the second-to-last field: "ip:host:container" and
		// "host:container" both put it there.
		return toPort(parts[len(parts)-2])
	case map[string]any:
		if published, ok := e["published"]; ok {
			return toPortValue(published)
		}
		return 0
	}
	return 0
}

// composeContainerPort extracts a container-side port from an `expose:` entry
// or the `target` key of a long `ports:` mapping.
func composeContainerPort(entry any) int {
	switch e := entry.(type) {
	case int:
		return e
	case string:
		if i := strings.Index(e, "/"); i >= 0 {
			e = e[:i]
		}
		return toPort(e)
	case map[string]any:
		if target, ok := e["target"]; ok {
			return toPortValue(target)
		}
	}
	return 0
}

// packageManifest is the part of package.json that carries a port.
type packageManifest struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         map[string]string `json:"engines"`
}

func readPackageManifest(dir string) (packageManifest, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return packageManifest{}, false
	}
	var manifest packageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return packageManifest{}, false
	}
	return manifest, true
}

// portFlagPattern matches an explicit port passed as a flag, in shell form
// (`--port 8000`, `--port=8000`, `-p 3000`) and in the JSON-array form a
// Dockerfile CMD prefers (`["--port", "8000"]` – hence the quote and comma in
// the separator class). The gap is bounded so a distant number cannot be picked
// up, and the digit group is required, so `tsc -p tsconfig.json` and
// `--port-from-file` never match.
var portFlagPattern = regexp.MustCompile(`(?i)(?:--port|-p)["'=\s,]{0,5}(\d{2,5})\b`)

// nodeFrameworkPorts maps a package.json dependency to the port its server
// listens on by convention, most specific first. A miss simply falls through to
// the next detection source, so a wrong convention here costs a fallback, not a
// wrong deploy – the one exception being a hit that is itself wrong, which is
// why the list stays small and mainstream.
//
// Mind the dev/production split: `vite` is 5173 for `vite dev`, but a container
// usually runs `vite build` + a static server (or `vite preview`, 4173) – see
// vitePreviewPort below, which handles the start script first.
var nodeFrameworkPorts = []struct {
	dep  string
	port int
}{
	{"next", 3000},
	{"nuxt", 3000},
	{"@nestjs/core", 3000},
	{"@sveltejs/kit", 3000},
	{"@remix-run/serve", 3000},
	{"astro", 4321},
	{"vite", 5173},
	{"react-scripts", 3000},
	{"@vue/cli-service", 8080},
	{"@angular/cli", 4200},
	{"gatsby", 9000},
	{"serve", 3000},
	{"http-server", 8080},
	{"express", 3000},
}

// vitePreviewPort is what `vite preview` serves on, i.e. what a container
// running `npm start` after `vite build` exposes.
const vitePreviewPort = 4173

// detectNodePort resolves the port from package.json: an explicit port in a
// script wins, otherwise the framework's conventional port. Node projects are
// the main beneficiaries – they rarely declare EXPOSE.
func detectNodePort(dir string) (int, string, bool) {
	if port, source, ok := detectNodeScriptPort(dir); ok {
		return port, source, true
	}
	return detectNodeFrameworkPort(dir)
}

// detectNodeScriptPort reads an explicit port out of a package.json script.
func detectNodeScriptPort(dir string) (int, string, bool) {
	manifest, ok := readPackageManifest(dir)
	if !ok {
		return 0, "", false
	}
	scripts := make([]string, 0, len(manifest.Scripts))
	for name := range manifest.Scripts {
		scripts = append(scripts, name)
	}
	sort.Strings(scripts)
	for _, name := range scripts {
		if m := portFlagPattern.FindStringSubmatch(manifest.Scripts[name]); m != nil {
			if port := toPort(m[1]); port > 0 {
				return port, "package.json script " + strconv.Quote(name), true
			}
		}
	}
	return 0, "", false
}

// detectNodeFrameworkPort applies the framework convention table. This is the
// weakest signal – a documented default, not a declaration – so it runs only
// after the explicit scripts and the source scan have come up empty.
func detectNodeFrameworkPort(dir string) (int, string, bool) {
	manifest, ok := readPackageManifest(dir)
	if !ok {
		return 0, "", false
	}
	// `vite dev` is 5173 but a production container runs `vite preview`, which
	// is 4173 – so the start script decides before the framework table does.
	if start, ok := manifest.Scripts["start"]; ok && strings.Contains(start, "preview") {
		return vitePreviewPort, `package.json script "start" (vite preview)`, true
	}
	for _, fw := range nodeFrameworkPorts {
		_, prod := manifest.Dependencies[fw.dep]
		_, dev := manifest.DevDependencies[fw.dep]
		if prod || dev {
			return fw.port, "package.json dependency " + strconv.Quote(fw.dep), true
		}
	}
	return 0, "", false
}

// sourceScanLimits bound the source scan. `init` must stay instant on a
// monorepo, and a guess buried in the tree is worth less than a fast answer
// plus an honest "nothing detected".
const (
	portScanMaxFiles = 400
	portScanMaxBytes = 4 << 20
)

// scanSkipDirs never contain the project's own listen port.
var scanSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, ".next": true, ".nuxt": true, ".svelte-kit": true, ".venv": true,
	"venv": true, "__pycache__": true, "coverage": true, ".terraform": true,
}

// scanExtensions limits the walk to files that plausibly declare a port. JSON
// and lockfiles are excluded on purpose: they are full of numbers that are not
// ports.
var scanExtensions = map[string]bool{
	".go": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true,
	".jsx": true, ".py": true, ".rb": true, ".rs": true, ".php": true, ".java": true,
	".kt": true, ".sh": true, ".conf": true, ".properties": true, ".env": true,
}

// scanNames are extension-less files that routinely carry a port. A Procfile is
// the canonical example: `web: gunicorn -b 0.0.0.0:8000` is the only place
// some deployments declare it. The Dockerfile is scanned for the same reason –
// `CMD ["uvicorn", "main:app", "--port", "8000"]` often states the port when
// EXPOSE does not.
var scanNames = map[string]bool{
	"Procfile": true, "Procfile.dev": true, "Makefile": true, "Dockerfile": true,
}

// sourcePortPatterns are tried in order inside each file. They are deliberately
// few and specific: a generic "any four digits" match would latch onto version
// numbers, timeouts and constants.
var sourcePortPatterns = []*regexp.Regexp{
	// Go: http.ListenAndServe(":9090", nil), &http.Server{Addr: ":8080"}
	regexp.MustCompile(`ListenAndServe\(\s*"?:(\d{2,5})"`),
	regexp.MustCompile(`Addr:\s*":(\d{2,5})"`),
	// Anywhere: PORT=3000, PORT: 3000, PORT || 3000
	regexp.MustCompile(`\bPORT\s*[:=]\s*(\d{2,5})\b`),
	// Explicit CLI flag, wherever it appears: `uvicorn main:app --port 8000`,
	// `node server.js --port=3000`. See portFlagPattern for the forms.
	portFlagPattern,
	// Python keyword argument: uvicorn.run(app, port=8000), app.run(port=5000).
	// Broader than the others, so it sits late in the list – a numeric
	// `port = ` in a Python file is almost always a port, but "almost" is why
	// the specific forms above get first refusal.
	regexp.MustCompile(`(?i)\bport\s*=\s*(\d{2,5})\b`),
	// Node: app.listen(3000)
	regexp.MustCompile(`\.listen\(\s*(\d{2,5})`),
	// Django: manage.py runserver 0.0.0.0:8000
	regexp.MustCompile(`runserver\s+(?:[\d.]+:)?(\d{2,5})\b`),
	// gunicorn/uWSGI: -b 0.0.0.0:8000
	regexp.MustCompile(`(?m)\s-b\s+[\d.]*:(\d{2,5})\b`),
	// Spring: server.port=8080
	regexp.MustCompile(`\bserver\.port\s*=\s*(\d{2,5})\b`),
}

// pythonFrameworkPorts maps a Python dependency to the port its server listens
// on by convention, servers first: a container runs uvicorn/gunicorn rather
// than `flask run`, so those answers describe the deployed process, while
// `flask`→5000 and `django`→8000 are the framework dev defaults.
var pythonFrameworkPorts = []struct {
	dep  string
	port int
}{
	{"uvicorn", 8000},
	{"gunicorn", 8000},
	{"hypercorn", 8000},
	{"daphne", 8000},
	{"waitress", 8080},
	{"fastapi", 8000},
	{"flask", 5000},
	{"django", 8000},
	{"sanic", 8000},
	{"aiohttp", 8080},
	{"pyramid", 6543},
	{"bottle", 8080},
	{"tornado", 8888},
	{"streamlit", 8501},
	{"gradio", 7860},
}

// detectPythonPort resolves the port from the declared Python dependencies.
// Requirements files are the only place a Python project names its server, so
// this is the Node equivalent of the package.json framework table.
func detectPythonPort(dir string) (int, string, bool) {
	deps := pythonDependencies(dir)
	if len(deps) == 0 {
		return 0, "", false
	}
	for _, fw := range pythonFrameworkPorts {
		if deps[fw.dep] {
			return fw.port, "Python dependency " + strconv.Quote(fw.dep), true
		}
	}
	return 0, "", false
}

// requirementsNamePattern matches one requirement line: "flask==3.0.0",
// "fastapi>=0.1", "uvicorn[standard]", "django", "celery[redis] ; python_version>'3'".
var requirementsNamePattern = regexp.MustCompile(`(?m)^\s*-[a-zA-Z]\S*\s+([A-Za-z0-9._-]+)|^\s*([A-Za-z0-9._-]+)`)

// pythonDependencies collects declared distribution names from the manifests a
// Python project actually uses. Names are lowercased and stripped of extras,
// versions and the `-r` include indirection.
func pythonDependencies(dir string) map[string]bool {
	deps := map[string]bool{}
	add := func(name string) {
		// Keep only the distribution name: strip extras (`uvicorn[standard]`),
		// version specifiers (`django>=5.0`, `flask==3.0.0`), markers and
		// environment markers.
		if i := strings.IndexAny(name, "[=<>!~; \t\r\n"); i >= 0 {
			name = name[:i]
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return
		}
		deps[name] = true
	}

	// requirements*.txt – the most common form.
	requirements, _ := filepath.Glob(filepath.Join(dir, "requirements*.txt"))
	for _, path := range requirements {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "-") {
				continue // -r include, -e editable, --index-url …
			}
			if m := requirementsNamePattern.FindStringSubmatch(line); m != nil {
				if m[2] != "" {
					add(m[2])
				} else if m[1] != "" {
					add(m[1])
				}
			}
		}
	}

	// pyproject.toml – dependencies = ["fastapi", …]; inline and multi-line.
	if data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		if list := tomlArrayValue(data, "dependencies"); len(list) > 0 {
			for _, item := range list {
				add(item)
			}
		}
	}

	// Pipfile – flask = "*"
	if data, err := os.ReadFile(filepath.Join(dir, "Pipfile")); err == nil {
		for _, m := range regexp.MustCompile(`(?m)^\s*"?([A-Za-z0-9._-]+)"?\s*=`).FindAllStringSubmatch(string(data), -1) {
			if !strings.EqualFold(m[1], "packages") && !strings.EqualFold(m[1], "dev-packages") {
				add(m[1])
			}
		}
	}

	// setup.py – install_requires=["flask"]
	if data, err := os.ReadFile(filepath.Join(dir, "setup.py")); err == nil {
		if list := tomlArrayValue(data, "install_requires"); len(list) > 0 {
			for _, item := range list {
				add(item)
			}
		}
	}
	return deps
}

// tomlArrayValue pulls the quoted strings out of a `key = ["a", "b"]` array,
// tolerating a multi-line array. It is deliberately a scanner and not a TOML
// parse: setup.py is Python, and the goal is only to notice a dependency name.
func tomlArrayValue(data []byte, key string) []string {
	idx := strings.Index(string(data), key)
	if idx < 0 {
		return nil
	}
	rest := string(data)[idx+len(key):]
	open := strings.Index(rest, "[")
	if open < 0 {
		return nil
	}
	rest = rest[open+1:]
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil
	}
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(rest[:end], -1) {
		out = append(out, m[1])
	}
	return out
}

// detectSourcePort is the last resort before the default: a bounded scan of the
// project's own source for a literal listen port.
//
// It is here because manifests are not enough. A Go binary takes its port from
// a flag or the environment – go.mod has no port field and never will, since the
// language deliberately has no manifest-level default – so the port lives in
// main.go and the only way to find it is to look.
func detectSourcePort(dir string) (int, bool) {
	files := collectScanFiles(dir)
	read := 0
	for _, rel := range files {
		if read > portScanMaxBytes {
			return 0, false
		}
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		read += len(data)
		for _, re := range sourcePortPatterns {
			if m := re.FindSubmatch(data); m != nil {
				if port := toPort(string(m[1])); port > 0 {
					return port, true
				}
			}
		}
	}
	return 0, false
}

// collectScanFiles returns the sorted, capped list of files to scan.
func collectScanFiles(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil || rel == "." {
			return nil
		}
		if entry.IsDir() {
			if scanSkipDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		// `.env`, `.env.local` … carry `PORT=` and are matched by their
		// prefix; `Procfile` has no extension at all.
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !scanExtensions[ext] && !strings.HasPrefix(name, ".env") && !scanNames[name] {
			return nil
		}
		files = append(files, rel)
		if len(files) >= portScanMaxFiles {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// detectStack identifies the project's primary language from its manifest. It
// is reported by `init` for the user's benefit and picks no configuration value
// on its own – a manifest cannot tell us the port for every ecosystem (see
// detectSourcePort), so the stack is context, not a decision.
func detectStack(dir string) string {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case has("go.mod"):
		return "go"
	case has("package.json"):
		return "node"
	case has("pyproject.toml"), has("requirements.txt"), has("manage.py"):
		return "python"
	case has("Cargo.toml"):
		return "rust"
	case has("Gemfile"):
		return "ruby"
	case has("composer.json"):
		return "php"
	}
	return ""
}

// genericDirNames say nothing about the app. For those a manifest name is a
// better `app.name` than "src" or "app".
var genericDirNames = map[string]bool{
	"src": true, "app": true, "apps": true, "project": true, "projects": true,
	"test": true, "tests": true, "tmp": true, "temp": true, "main": true,
	"new": true, "untitled": true, "myapp": true, "server": true, "service": true,
}

// goModulePattern matches the module line of a go.mod.
var goModulePattern = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// majorVersionSuffix matches the trailing major-version element of a Go module
// path: "github.com/acme/api/v2" → "github.com/acme/api".
var majorVersionSuffix = regexp.MustCompile(`/v[0-9]+(\.[0-9]+)*$`)

// goModuleName returns the last path element of the Go module path, without the
// major-version suffix. It names the service better than a generic directory
// does – but it carries no port, by design of the language, which is why the
// port comes from detectSourcePort.
func goModuleName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	m := goModulePattern.FindSubmatch(data)
	if m == nil {
		return ""
	}
	path := majorVersionSuffix.ReplaceAllString(strings.TrimSuffix(string(m[1]), "/"), "")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[i+1:]
	}
	return strings.TrimSuffix(path, ".git")
}

// npmPackageName returns the `name` field of package.json, if present.
func npmPackageName(dir string) string {
	manifest, ok := readPackageManifest(dir)
	if !ok {
		return ""
	}
	return manifest.Name
}

// detectAppName picks app.name: the directory name, unless it is one of the
// generic placeholders and a manifest offers something more specific.
func detectAppName(dir, base string) (string, string) {
	dirName := sanitizeAppName(base)
	if !genericDirNames[strings.ToLower(base)] {
		return dirName, "directory name"
	}
	for _, candidate := range []struct{ label, raw string }{
		{"go.mod module", goModuleName(dir)},
		{"package.json name", npmPackageName(dir)},
	} {
		if candidate.raw == "" {
			continue
		}
		if name := sanitizeAppName(candidate.raw); name != "" && name != "app" {
			return name, candidate.label
		}
	}
	return dirName, "directory name"
}

func validPort(port int) bool { return port >= 1 && port <= 65535 }

// toPort parses a port string, returning 0 when it is not a usable port.
func toPort(s string) int {
	port, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || !validPort(port) {
		return 0
	}
	return port
}

// toPortValue handles the int-or-string duality of YAML scalars.
func toPortValue(v any) int {
	switch e := v.(type) {
	case int:
		if validPort(e) {
			return e
		}
	case string:
		return toPort(e)
	}
	return 0
}
