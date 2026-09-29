// Command vendorweb writes the Web UI's third-party files from npm as a manifest names them: each package's tarball,
// checked against the registry's integrity, the listed files and the package's licence, with the one rewrite a file
// may declare (a bare import turned into a relative path). It records the upstream and the written checksums, which
// the server's tests hold the files to.
//
//	go run ./tools/vendorweb [manifest.json…]
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fileio"
)

var manifests = []string{"internal/server/web/vendor/manifest.json", "internal/server/webtest/vendor/manifest.json"}

type manifest struct {
	Packages []pkg `json:"packages"`
}

type pkg struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Integrity string `json:"integrity,omitempty"`
	License   file   `json:"license"`
	Files     []file `json:"files"`
}

type file struct {
	From     string            `json:"from"` // path inside the package
	To       string            `json:"to"`   // path next to the manifest
	Imports  map[string]string `json:"imports,omitempty"`
	Upstream string            `json:"upstream,omitempty"` // sha256 of the file as published
	SHA256   string            `json:"sha256,omitempty"`   // sha256 of the file as written
}

// importSpec finds module specifiers: from"x", import"x", import("x"), in minified or spaced code.
var importSpec = regexp.MustCompile(`(\bfrom\s*|\bimport\s*\(?\s*)(["'])([^"'\n]+)["']`)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = manifests
	}
	for _, m := range args {
		if err := vendor(m); err != nil {
			fmt.Fprintf(os.Stderr, "vendorweb: %s: %v\n", m, err)
			os.Exit(1)
		}
		fmt.Println("wrote", m)
	}
}

func vendor(manifestPath string) error {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	dir := filepath.Dir(manifestPath)
	for i := range m.Packages {
		if err := fetch(&m.Packages[i], dir); err != nil {
			return fmt.Errorf("%s@%s: %w", m.Packages[i].Name, m.Packages[i].Version, err)
		}
	}
	out, _ := json.MarshalIndent(m, "", "  ")
	return fileio.WriteFile(manifestPath, append(out, '\n'), 0o644)
}

func fetch(p *pkg, dir string) error {
	meta, err := registry(p.Name, p.Version)
	if err != nil {
		return err
	}
	if p.Integrity != "" && p.Integrity != meta.Dist.Integrity {
		return fmt.Errorf("the registry's integrity %s is not the manifest's %s", meta.Dist.Integrity, p.Integrity)
	}
	tarball, err := get(meta.Dist.Tarball)
	if err != nil {
		return err
	}
	sum := sha512.Sum512(tarball)
	if got := "sha512-" + base64.StdEncoding.EncodeToString(sum[:]); got != meta.Dist.Integrity {
		return fmt.Errorf("the tarball's integrity %s is not the registry's %s", got, meta.Dist.Integrity)
	}
	p.Integrity = meta.Dist.Integrity
	contents, err := untar(tarball)
	if err != nil {
		return err
	}
	if p.License.From == "" {
		p.License.From = "LICENSE"
	}
	if p.License.To == "" {
		p.License.To = "LICENSES/" + strings.ReplaceAll(strings.TrimPrefix(p.Name, "@"), "/", "-") + ".txt"
	}
	for _, f := range append([]*file{&p.License}, ptrs(p.Files)...) {
		src, ok := contents[f.From]
		if !ok {
			return fmt.Errorf("the package has no %s", f.From)
		}
		out, err := rewrite(src, f.Imports)
		if err != nil {
			return fmt.Errorf("%s: %w", f.From, err)
		}
		f.Upstream, f.SHA256 = hexSum(src), hexSum(out)
		to := filepath.Join(dir, filepath.FromSlash(f.To))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := fileio.WriteFile(to, out, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func ptrs(fs []file) []*file {
	out := make([]*file, len(fs))
	for i := range fs {
		out[i] = &fs[i]
	}
	return out
}

// rewrite turns each bare import the manifest declares into its relative path; any other bare import, or a declared
// one the file does not have, is an error.
func rewrite(src []byte, imports map[string]string) ([]byte, error) {
	hit := map[string]bool{}
	var bad []string
	out := importSpec.ReplaceAllStringFunc(string(src), func(s string) string {
		g := importSpec.FindStringSubmatch(s)
		spec := g[3]
		if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
			return s
		}
		to, ok := imports[spec]
		if !ok {
			bad = append(bad, spec)
			return s
		}
		hit[spec] = true
		return g[1] + g[2] + to + g[2]
	})
	if len(bad) > 0 {
		return nil, fmt.Errorf("undeclared bare imports %q", bad)
	}
	for spec := range imports {
		if !hit[spec] {
			return nil, fmt.Errorf("the declared import %q is not in the file", spec)
		}
	}
	return []byte(out), nil
}

type meta struct {
	Dist struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func registry(name, version string) (meta, error) {
	var m meta
	b, err := get("https://registry.npmjs.org/" + strings.Replace(url.PathEscape(name), "%40", "@", 1) + "/" + url.PathEscape(version))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.Dist.Tarball == "" || !strings.HasPrefix(m.Dist.Integrity, "sha512-") {
		return m, errors.New("the registry names no tarball with a sha512 integrity")
	}
	return m, nil
}

var client = &http.Client{Timeout: time.Minute}

func get(u string) ([]byte, error) {
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// untar reads a package tarball's regular files by their path under the package's top directory.
func untar(b []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(h.Name)
		if i := strings.IndexByte(name, '/'); i >= 0 && !slices.Contains(strings.Split(name, "/"), "..") {
			body, err := io.ReadAll(io.LimitReader(tr, 16<<20))
			if err != nil {
				return nil, err
			}
			out[name[i+1:]] = body
		}
	}
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
