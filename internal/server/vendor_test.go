package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type vendorFile struct {
	From, To, Upstream, SHA256 string
	Imports                    map[string]string
}

type vendorManifest struct {
	Packages []struct {
		Name, Version, Integrity string
		License                  vendorFile
		Files                    []vendorFile
	}
}

// The vendored files are what their manifests say (tools/vendorweb writes both): each file's checksum, a rewrite only
// where the manifest declares one, and nothing in the directory the manifest does not name.
func TestTheVendoredFilesAreTheManifests(t *testing.T) {
	for _, dir := range []string{"web/vendor", "webtest/vendor"} {
		b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m vendorManifest
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		named := map[string]bool{"manifest.json": true}
		for _, p := range m.Packages {
			if p.Version == "" || !strings.HasPrefix(p.Integrity, "sha512-") {
				t.Errorf("%s: version %q, integrity %q", p.Name, p.Version, p.Integrity)
			}
			for _, f := range append([]vendorFile{p.License}, p.Files...) {
				named[f.To] = true
				body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.To)))
				if err != nil {
					t.Errorf("%s: %v", p.Name, err)
					continue
				}
				sum := sha256.Sum256(body)
				if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
					t.Errorf("%s/%s: sha256 %s, the manifest says %s", dir, f.To, got, f.SHA256)
				}
				if len(f.Imports) == 0 && f.Upstream != f.SHA256 {
					t.Errorf("%s/%s differs from upstream without a declared rewrite", dir, f.To)
				}
				for spec, to := range f.Imports {
					if !strings.Contains(string(body), `"`+to+`"`) || slices.Contains(importsOf(string(body)), spec) {
						t.Errorf("%s/%s: %s is not rewritten to %s", dir, f.To, spec, to)
					}
				}
			}
		}
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			if !named[filepath.ToSlash(rel)] {
				t.Errorf("%s/%s is not in the manifest", dir, filepath.ToSlash(rel))
			}
			return nil
		})
	}
}

var importSpec = regexp.MustCompile(`(\bfrom\s*|\bimport\s*\(?\s*)["']([^"'\n]+)["']`)

func importsOf(src string) []string {
	var out []string
	for _, m := range importSpec.FindAllStringSubmatch(src, -1) {
		out = append(out, m[2])
	}
	return out
}

// layers: what a directory of web/ may import. The page's entry (web/main.js) may import any of them.
var layers = map[string][]string{
	"vendor": {"vendor"},
	"core":   {"core", "vendor"},
	"ui":     {"ui", "core", "vendor"},
	"pages":  {"pages", "ui", "core", "vendor"},
}

func layerOf(rel string) string {
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return ""
}

// Every import in web/ is a relative path to a file that is there, and runs down the layers pages → ui → core →
// vendor; ui never reaches the wire itself. The test scripts import only web/, each other and node's own modules.
func TestImportsAreRelativeAndRunDownTheLayers(t *testing.T) {
	check := func(root string, allowed func(from, to, spec string) bool) int {
		n := 0
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".mjs")) {
				return err
			}
			b, _ := os.ReadFile(p)
			from := filepath.ToSlash(p)
			for _, spec := range importsOf(string(b)) {
				n++
				if strings.HasPrefix(spec, "node:") && strings.HasPrefix(from, "webtest/") {
					continue
				}
				if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
					t.Errorf("%s imports %q: not a relative path", from, spec)
					continue
				}
				to := path.Join(path.Dir(from), spec)
				if _, err := os.Stat(filepath.FromSlash(to)); err != nil {
					t.Errorf("%s imports %q: %v", from, spec, err)
					continue
				}
				if !allowed(from, to, spec) {
					t.Errorf("%s may not import %s", from, to)
				}
			}
			return nil
		})
		return n
	}
	web := check("web", func(from, to, _ string) bool {
		if !strings.HasPrefix(to, "web/") {
			return false
		}
		fl, tl := layerOf(strings.TrimPrefix(from, "web/")), layerOf(strings.TrimPrefix(to, "web/"))
		if fl == "ui" && to == "web/core/wire.js" {
			return false
		}
		return fl == "" || slices.Contains(layers[fl], tl)
	})
	test := check("webtest", func(_, to, _ string) bool { return strings.HasPrefix(to, "web/") || strings.HasPrefix(to, "webtest/") })
	if web < 5 || test < 10 {
		t.Fatalf("%d imports in web/, %d in webtest/", web, test)
	}
}

func TestTheModulesAndStylesAreServedByType(t *testing.T) {
	r := newRig(t)
	for p, kind := range map[string]string{"/vendor/preact.mjs": "text/javascript", "/core/wire.js": "text/javascript", "/ui/shell.js": "text/javascript", "/css/base.css": "text/css"} {
		resp, err := http.Get(r.url + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), kind) {
			t.Errorf("%s: %d %s", p, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}
