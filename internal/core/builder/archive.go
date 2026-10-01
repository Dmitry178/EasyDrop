package builder

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// archiveFileName is the bundle name shipped to the host. It is always
// excluded from the archive itself (even without a .dockerignore).
const archiveFileName = "project.tar.gz"

// defaultExcludes always applies on top of .dockerignore rules, which can
// still re-include any of them with a "!" negation.
//
// The two dotenv files are excluded because easydrop itself reads them for
// ${VAR} interpolation (see internal/config/env.go) – a deploy must not carry
// the credentials it authenticates with to the target host. A project that
// really needs its .env inside the bundle (docker compose variable
// substitution) re-includes it with a ".env" line negated in .dockerignore.
var defaultExcludes = []string{
	".git",
	"node_modules",
	archiveFileName,
	".env",
	".easydrop.env",
}

// ignorePattern is one compiled .dockerignore / default line.
type ignorePattern struct {
	neg bool
	re  *regexp.Regexp
}

// compileIgnorePattern converts a dockerignore-style glob into a matcher for
// slash-separated relative paths. Supported syntax: `*`, `?`, `**`, leading
// `/`, trailing `/` (dir-only), `!` negation (handled by the caller).
// A pattern matching a directory excludes everything under it.
func compileIgnorePattern(pattern string) (*regexp.Regexp, error) {
	p := strings.TrimSpace(pattern)
	p = strings.TrimSuffix(p, "/")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil, fmt.Errorf("empty ignore pattern")
	}

	var body strings.Builder
	for i := 0; i < len(p); {
		switch {
		case strings.HasPrefix(p[i:], "**/"):
			body.WriteString("(.*/)?")
			i += 3
		case p[i] == '*':
			if i+1 < len(p) && p[i+1] == '*' {
				body.WriteString(".*")
				i += 2
			} else {
				body.WriteString("[^/]*")
				i++
			}
		case p[i] == '?':
			body.WriteString("[^/]")
			i++
		default:
			body.WriteString(regexp.QuoteMeta(string(p[i])))
			i++
		}
	}
	var full string
	if strings.Contains(p, "/") {
		// Anchored: match from the workspace root.
		full = "^(" + body.String() + ")(/.*)?$"
	} else {
		// Basename: match at any depth (docker semantics).
		full = "^(.*/)?(" + body.String() + ")(/.*)?$"
	}
	return regexp.Compile(full)
}

// ignoreRules is the ordered rule set; last matching rule wins (docker semantics).
type ignoreRules struct {
	patterns []ignorePattern
}

// loadIgnoreRules reads .dockerignore from srcDir (absent file = no rules,
// which is not an error) and appends the default excludes first so user
// rules can re-include via `!` negation.
func loadIgnoreRules(srcDir string) (*ignoreRules, error) {
	r := &ignoreRules{}
	for _, p := range defaultExcludes {
		re, err := compileIgnorePattern(p)
		if err != nil {
			return nil, err
		}
		r.patterns = append(r.patterns, ignorePattern{re: re})
	}
	data, err := os.ReadFile(filepath.Join(srcDir, ".dockerignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("read .dockerignore: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := strings.HasPrefix(line, "!")
		re, err := compileIgnorePattern(strings.TrimPrefix(line, "!"))
		if err != nil {
			return nil, fmt.Errorf("bad .dockerignore line %q: %w", line, err)
		}
		r.patterns = append(r.patterns, ignorePattern{neg: neg, re: re})
	}
	return r, nil
}

// excluded reports whether the slash-separated relative path is ignored.
func (r *ignoreRules) excluded(rel string) bool {
	hit := false
	for _, p := range r.patterns {
		if p.re.MatchString(rel) {
			hit = !p.neg
		}
	}
	return hit
}

// CreateProjectArchive compresses srcDir into a tar.gz stream: it honors
// .dockerignore (with `!` re-inclusion), always drops .git, node_modules,
// project.tar.gz itself and the .env / .easydrop.env files easydrop reads
// secrets from, and packs regular files only (symlinks, sockets and
// other specials are skipped). File modes are preserved.
func CreateProjectArchive(srcDir string, w io.Writer) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("stat workspace %q: %w", srcDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace %q is not a directory", srcDir)
	}
	rules, err := loadIgnoreRules(srcDir)
	if err != nil {
		return err
	}

	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if rules.excluded(relSlash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() && !d.IsDir() {
			return nil // skip symlinks, sockets, etc.
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		header.Name = relSlash
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		tw.Close()
		gw.Close()
		return fmt.Errorf("archive workspace %q: %w", srcDir, walkErr)
	}
	if err := tw.Close(); err != nil {
		gw.Close()
		return fmt.Errorf("close tar stream: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip stream: %w", err)
	}
	return nil
}
