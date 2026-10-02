package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// scaffoldIn builds a project directory with the given base name – so
// app.name detection is exercised with a controlled directory name – and runs
// Scaffold on it.
func scaffoldIn(t *testing.T, name string, files map[string]string) *ScaffoldResult {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Scaffold(dir)
	if err != nil {
		t.Fatalf("Scaffold() unexpected error: %v", err)
	}
	return res
}

// scaffoldOn builds an unremarkable project directory and runs Scaffold.
func scaffoldOn(t *testing.T, files map[string]string) *ScaffoldResult {
	t.Helper()
	return scaffoldIn(t, "myapp", files)
}

func TestDetectPortSources(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		wantPort   int
		wantSource string
	}{
		{
			name: "Dockerfile EXPOSE wins for the single driver",
			files: map[string]string{
				"Dockerfile":   "FROM node:22\nEXPOSE 3000\n",
				"package.json": `{"dependencies":{"next":"15.1.0"}}`,
				"main.go":      `package main` + "\n" + `// 9999`,
			},
			wantPort: 3000, wantSource: PortSourceExpose,
		},
		{
			name:       "last EXPOSE wins in a multi-stage build",
			files:      map[string]string{"Dockerfile": "FROM node:22 AS build\nEXPOSE 3000\nFROM node:22\nEXPOSE 8080\n"},
			wantPort:   8080,
			wantSource: PortSourceExpose,
		},
		{
			name:       "package.json script port beats the framework convention",
			files:      map[string]string{"package.json": `{"scripts":{"start":"next start -p 4000"},"dependencies":{"next":"15.1.0"}}`},
			wantPort:   4000,
			wantSource: `package.json script "start"`,
		},
		{
			name:       "package.json framework convention",
			files:      map[string]string{"package.json": `{"dependencies":{"next":"15.1.0"}}`},
			wantPort:   3000,
			wantSource: `package.json dependency "next"`,
		},
		{
			name:       "devDependency counts too",
			files:      map[string]string{"package.json": `{"devDependencies":{"vite":"6.0.0"}}`},
			wantPort:   5173,
			wantSource: `package.json dependency "vite"`,
		},
		{
			name:       "Vue CLI",
			files:      map[string]string{"package.json": `{"dependencies":{"@vue/cli-service":"5.0.8"}}`},
			wantPort:   8080,
			wantSource: `package.json dependency "@vue/cli-service"`,
		},
		{
			name:       "Angular CLI",
			files:      map[string]string{"package.json": `{"devDependencies":{"@angular/cli":"18.0.0"}}`},
			wantPort:   4200,
			wantSource: `package.json dependency "@angular/cli"`,
		},
		{
			// A production container runs `vite preview` (4173) after
			// `vite build`, not the 5173 dev server.
			name:       "vite preview in the start script",
			files:      map[string]string{"package.json": `{"scripts":{"dev":"vite","build":"vite build","start":"vite preview"},"devDependencies":{"vite":"6.0.0"}}`},
			wantPort:   4173,
			wantSource: `package.json script "start" (vite preview)`,
		},
		{
			name:       "tsc -p tsconfig must not be read as a port",
			files:      map[string]string{"package.json": `{"scripts":{"build":"tsc -p tsconfig.json"}}`},
			wantPort:   0,
			wantSource: PortSourceUnknown,
		},
		{
			name:       "Go ListenAndServe in source",
			files:      map[string]string{"go.mod": "module example.com/x\n\ngo 1.23\n", "main.go": "package main\n\nfunc main() {\n\thttp.ListenAndServe(\":9090\", nil)\n}\n"},
			wantPort:   9090,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "Go http.Server Addr in source",
			files:      map[string]string{"main.go": "package main\n\nvar srv = &http.Server{Addr: \":7070\"}\n"},
			wantPort:   7070,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "PORT= in a .env file",
			files:      map[string]string{".env": "DATABASE_URL=postgres://x\nPORT=5050\n"},
			wantPort:   5050,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "app.listen in JS",
			files:      map[string]string{"server.js": "app.listen(3333);\n"},
			wantPort:   3333,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "Django runserver",
			files:      map[string]string{"manage.py": "os.environ.setdefault(\n}, execute_from_command_line\n", "Procfile": "web: python manage.py runserver 0.0.0.0:8000\n"},
			wantPort:   8000,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "gunicorn bind address",
			files:      map[string]string{"Procfile": "web: gunicorn app.wsgi -b 0.0.0.0:8010 --workers 2\n"},
			wantPort:   8010,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "FastAPI uvicorn.run keyword argument",
			files:      map[string]string{"main.py": "import uvicorn\n\nif __name__ == \"__main__\":\n    uvicorn.run(app, host=\"0.0.0.0\", port=8000)\n"},
			wantPort:   8000,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "FastAPI --port on a Procfile",
			files:      map[string]string{"Procfile": "web: uvicorn main:app --host 0.0.0.0 --port 8000\n"},
			wantPort:   8000,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "--port in a Dockerfile CMD, no EXPOSE",
			files:      map[string]string{"Dockerfile": "FROM python:3.12-slim\nCMD [\"uvicorn\", \"main:app\", \"--port\", \"8000\"]\n", "main.py": "app = 1\n"},
			wantPort:   8000,
			wantSource: PortSourceDockerCmd,
		},
		{
			// The image pins the port, so the framework convention must lose.
			name: "a Dockerfile CMD flag outranks the framework convention",
			files: map[string]string{
				"package.json": `{"scripts":{"start":"next start"},"dependencies":{"next":"15.1.0"}}`,
				"Dockerfile":   "FROM node:22\nCMD [\"next\", \"start\", \"-p\", \"4000\"]\n",
			},
			wantPort: 4000, wantSource: PortSourceDockerCmd,
		},
		{
			name: "ENV PORT in a Dockerfile",
			files: map[string]string{
				"Dockerfile": "FROM node:22\nENV PORT=4321\nCMD [\"node\", \"server.js\"]\n",
			},
			wantPort: 4321, wantSource: PortSourceDockerCmd,
		},
		{
			name:       "Flask default is 5000, not 8080",
			files:      map[string]string{"requirements.txt": "flask==3.0.0\n", "app.py": "app.run()\n"},
			wantPort:   5000,
			wantSource: `Python dependency "flask"`,
		},
		{
			name:       "uvicorn serves on 8000",
			files:      map[string]string{"requirements.txt": "fastapi\nuvicorn[standard]\n"},
			wantPort:   8000,
			wantSource: `Python dependency "uvicorn"`,
		},
		{
			name:       "a container runs gunicorn, so 8000 beats flask's dev 5000",
			files:      map[string]string{"requirements.txt": "flask\ngunicorn\n"},
			wantPort:   8000,
			wantSource: `Python dependency "gunicorn"`,
		},
		{
			name:       "Django from pyproject",
			files:      map[string]string{"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\"django>=5.0\", \"psycopg2\"]\n"},
			wantPort:   8000,
			wantSource: `Python dependency "django"`,
		},
		{
			name:       "an explicit Flask port still wins over the convention",
			files:      map[string]string{"requirements.txt": "flask\n", "app.py": "app.run(port=5001)\n"},
			wantPort:   5001,
			wantSource: PortSourceSourceScan,
		},
		{
			name:       "a non-Python dependency must not trigger the map",
			files:      map[string]string{"requirements.txt": "requests\npsycopg2-binary\n"},
			wantPort:   0,
			wantSource: PortSourceUnknown,
		},
		{
			name: "a static-serve final stage outranks the framework convention",
			files: map[string]string{
				"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"6.0.0"}}`,
				"Dockerfile":   "FROM node:22 AS build\nRUN npm run build\nFROM nginx:alpine\nCOPY --from=build /app/dist /usr/share/nginx/html\n",
			},
			wantPort: 80, wantSource: PortSourceDockerRuntime,
		},
		{
			name: "EXPOSE outranks the final-stage inference",
			files: map[string]string{
				"Dockerfile": "FROM nginx:alpine\nEXPOSE 8080\n",
			},
			wantPort: 8080, wantSource: PortSourceExpose,
		},
		{
			name: "a non-server final stage infers nothing",
			files: map[string]string{
				"package.json": `{"dependencies":{"next":"15.1.0"}}`,
				"Dockerfile":   "FROM node:22 AS build\nRUN npm ci\nFROM node:22-slim\nCMD [\"npm\", \"start\"]\n",
			},
			wantPort: 3000, wantSource: `package.json dependency "next"`,
		},
		{
			name:       "a database port variable must not be mistaken for the app port",
			files:      map[string]string{"settings.py": "DB_PORT = 5432\nserial_port = 9600\n"},
			wantPort:   0,
			wantSource: PortSourceUnknown,
		},
		{
			name:       "source scan ignores node_modules and vendor",
			files:      map[string]string{"node_modules/dep/index.js": "app.listen(9999);\n", "vendor/x/x.go": `ListenAndServe(":8888")`},
			wantPort:   0,
			wantSource: PortSourceUnknown,
		},
		{
			name:       "source scan ignores version-looking numbers",
			files:      map[string]string{"main.go": "package main\n\nconst version = \"20240101\"\nconst timeout = 30000\n"},
			wantPort:   0,
			wantSource: PortSourceUnknown,
		},
		{
			name:       "nothing detectable falls back to 8080",
			files:      map[string]string{"README.md": "hi\n"},
			wantPort:   0,
			wantSource: PortSourceUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := scaffoldOn(t, tc.files)
			if res.App.Port != tc.wantPort {
				t.Errorf("App.Port = %d (%s), want %d", res.App.Port, res.PortSource, tc.wantPort)
			}
			if res.PortSource != tc.wantSource {
				t.Errorf("PortSource = %q, want %q", res.PortSource, tc.wantSource)
			}
			if want := tc.wantSource == PortSourceUnknown; res.PortMissing() != want {
				t.Errorf("PortMissing() = %v, want %v", res.PortMissing(), want)
			}
		})
	}
}

func TestDetectComposePort(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		wantPort   int
		wantSource string
	}{
		{
			name: "host side of a short mapping",
			files: map[string]string{
				"docker-compose.yml": "services:\n  web:\n    image: x\n    ports:\n      - \"8080:80\"\n",
			},
			wantPort: 8080, wantSource: PortSourceComposePort + " docker-compose.yml (web)",
		},
		{
			name: "host side with a bound IP",
			files: map[string]string{
				"docker-compose.yml": "services:\n  api:\n    image: x\n    ports:\n      - \"127.0.0.1:9000:9000\"\n",
			},
			wantPort: 9000, wantSource: PortSourceComposePort + " docker-compose.yml (api)",
		},
		{
			name: "long syntax with published",
			files: map[string]string{
				"docker-compose.yml": "services:\n  web:\n    ports:\n      - target: 80\n        published: \"8081\"\n",
			},
			wantPort: 8081, wantSource: PortSourceComposePort + " docker-compose.yml (web)",
		},
		{
			name: "protocol suffix is ignored",
			files: map[string]string{
				"docker-compose.yml": "services:\n  web:\n    ports:\n      - \"7000:7000/udp\"\n",
			},
			wantPort: 7000, wantSource: PortSourceComposePort + " docker-compose.yml (web)",
		},
		{
			name: "preferred service name wins over alphabetical",
			files: map[string]string{
				"docker-compose.yml": "services:\n  alpha:\n    ports: [\"1111:1111\"]\n  web:\n    ports: [\"2222:2222\"]\n",
			},
			wantPort: 2222, wantSource: PortSourceComposePort + " docker-compose.yml (web)",
		},
		{
			name: "ephemeral host port falls back to expose",
			files: map[string]string{
				"docker-compose.yml": "services:\n  web:\n    ports:\n      - \"80\"\n    expose:\n      - \"3000\"\n",
			},
			wantPort: 3000, wantSource: PortSourceComposeExp + " docker-compose.yml (web)",
		},
		{
			name: "compose mapping outranks EXPOSE for the compose driver",
			files: map[string]string{
				"docker-compose.yml": "services:\n  web:\n    ports:\n      - \"8080:3000\"\n",
				"Dockerfile":         "FROM node:22\nEXPOSE 3000\n",
			},
			wantPort: 8080, wantSource: PortSourceComposePort + " docker-compose.yml (web)",
		},
		{
			name: "broken YAML degrades to the next source",
			files: map[string]string{
				"docker-compose.yml": "services:\n  web:\n   ports:\n  - [unclosed\n",
				"Dockerfile":         "FROM node:22\nEXPOSE 3000\n",
			},
			wantPort: 3000, wantSource: PortSourceExpose,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := scaffoldOn(t, tc.files)
			if res.Driver.Type != "compose" {
				t.Fatalf("Driver.Type = %q, want compose", res.Driver.Type)
			}
			if res.App.Port != tc.wantPort {
				t.Errorf("App.Port = %d (%s), want %d", res.App.Port, res.PortSource, tc.wantPort)
			}
			if res.PortSource != tc.wantSource {
				t.Errorf("PortSource = %q, want %q", res.PortSource, tc.wantSource)
			}
		})
	}
}

// TestComposeFileDetection pins the fix: the compose file init found must be
// the one `driver.compose_file` names, for every spelling.
func TestComposeFileDetection(t *testing.T) {
	for _, name := range composeFileNames {
		t.Run(name, func(t *testing.T) {
			res := scaffoldOn(t, map[string]string{name: "services:\n  web:\n    image: x\n"})
			if res.Driver.ComposeFile != name {
				t.Errorf("ComposeFile = %q, want %q", res.Driver.ComposeFile, name)
			}
		})
	}
}

func TestDetectStack(t *testing.T) {
	cases := []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{"go.mod": "module x\n\ngo 1.23\n"}, "go"},
		{map[string]string{"package.json": "{}"}, "node"},
		{map[string]string{"pyproject.toml": "[project]\nname='x'\n"}, "python"},
		{map[string]string{"requirements.txt": "flask\n"}, "python"},
		{map[string]string{"Cargo.toml": "[package]\nname='x'\n"}, "rust"},
		{map[string]string{"Gemfile": "source 'x'\n"}, "ruby"},
		{map[string]string{"composer.json": "{}"}, "php"},
		{map[string]string{"Dockerfile": "FROM scratch\n"}, ""},
	}
	for _, tc := range cases {
		res := scaffoldOn(t, tc.files)
		if res.Stack != tc.want {
			t.Errorf("detectStack(%v) = %q, want %q", tc.files, res.Stack, tc.want)
		}
	}
}

func TestDetectAppName(t *testing.T) {
	// A descriptive directory name wins – a manifest is only a fallback.
	res := scaffoldIn(t, "billing-api", map[string]string{
		"go.mod": "module github.com/other/other-service\n\ngo 1.23\n",
	})
	if res.App.Name != "billing-api" {
		t.Errorf("App.Name = %q, want the directory name", res.App.Name)
	}
	if res.AppNameSource != "directory name" {
		t.Errorf("AppNameSource = %q, want directory name", res.AppNameSource)
	}
}

func TestDetectAppNameFromGenericDir(t *testing.T) {
	// A generic directory name is uninformative; the Go module path names the
	// service better (major-version suffix stripped). go.mod carries no port –
	// the language has no manifest-level default – hence the source scan.
	res := scaffoldIn(t, "src", map[string]string{
		"go.mod":  "module github.com/acme/billing-api/v2\n\ngo 1.23\n",
		"main.go": "package main\n\nfunc main() { http.ListenAndServe(\":9091\", nil) }\n",
	})
	if res.App.Name != "billing-api" {
		t.Errorf("App.Name = %q, want billing-api from the go.mod module", res.App.Name)
	}
	if res.AppNameSource != "go.mod module" {
		t.Errorf("AppNameSource = %q, want go.mod module", res.AppNameSource)
	}
	if res.App.Port != 9091 {
		t.Errorf("App.Port = %d, want 9091 from the source scan", res.App.Port)
	}
}

func TestDetectAppNameFromPackageJSON(t *testing.T) {
	res := scaffoldIn(t, "app", map[string]string{
		"package.json": `{"name":"@acme/storefront"}`,
	})
	if res.App.Name != "acme-storefront" {
		t.Errorf("App.Name = %q, want acme-storefront", res.App.Name)
	}
	if res.AppNameSource != "package.json name" {
		t.Errorf("AppNameSource = %q, want package.json name", res.AppNameSource)
	}
}

func TestScaffoldNeverFailsOnGarbage(t *testing.T) {
	// Detection reads untrusted project files; malformed ones must degrade to
	// the next source, never fail `init`.
	files := map[string]string{
		"go.mod":             "this is not a go.mod at all\n",
		"package.json":       "{not json",
		"docker-compose.yml": "\t\t- broken: [",
		"Dockerfile":         "EXPOSE not-a-number\n",
	}
	res := scaffoldOn(t, files)
	if res.App.Port != 0 {
		t.Errorf("App.Port = %d, want 0 for unusable input (nothing is invented)", res.App.Port)
	}
	if res.PortSource != PortSourceUnknown {
		t.Errorf("PortSource = %q, want %q", res.PortSource, PortSourceUnknown)
	}
}

// TestScaffoldOmitsPortWhenUndetected is the OD-04 guard: the generated file
// must not carry a port at all, and the parser must then refuse it with an
// actionable message instead of silently deploying a wrong mapping.
func TestScaffoldOmitsPortWhenUndetected(t *testing.T) {
	dir := mkScaffoldDir(t, map[string]string{"README.md": "no port here\n"})
	res, err := Scaffold(dir)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	path := filepath.Join(dir, "easydrop.toml")
	if err := WriteConfig(path, res.Config, false); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(written)
	var raw map[string]map[string]any
	if err := toml.Unmarshal(written, &raw); err != nil {
		t.Fatalf("decode written config: %v", err)
	}
	for _, key := range []string{"port", "host_port"} {
		if _, present := raw["app"][key]; present {
			t.Errorf("written config must omit [app].%s, got:\n%s", key, body)
		}
	}
	// Every other default must still be present, so the file is one edit away
	// from working.
	for _, want := range []string{"name =", "host = 'localhost'", "strategy = 'remote'", "type = 'single'"} {
		if !strings.Contains(body, want) {
			t.Errorf("written config missing %q:\n%s", want, body)
		}
	}
	if got := raw["server"]["port"]; got != int64(22) {
		t.Errorf("[server].port = %v, want the 22 default", got)
	}
	// And it must fail to parse, loudly and specifically.
	if _, err := ParseConfig(path); err == nil {
		t.Fatal("a config with no app.port must not parse")
	} else {
		for _, want := range []string{"app.port is required", "healthcheck", "502"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error must mention %q, got: %v", want, err)
			}
		}
	}
}
