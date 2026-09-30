package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// The markers the server fills in are in the files, so what it serves names the build.
func TestThePageAndItsWorkerNameTheBuild(t *testing.T) {
	for file, marker := range map[string]string{"web/index.html": buildMeta, "web/sw.js": swBoot} {
		if b, _ := fs.ReadFile(webFiles, file); bytes.Count(b, []byte(marker)) != 1 {
			t.Errorf("%s does not hold %s once", file, marker)
		}
	}
	r := newRig(t)
	resp, b := get(t, r.url+"/")
	if resp.StatusCode != 200 || !strings.Contains(string(b), `<meta name="tend-build" content="`+Build()+`">`) || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	resp, b = get(t, r.url+"/sw.js")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") || resp.Header.Get("Cache-Control") != "no-cache" ||
		!strings.Contains(string(b), `"build":"`+Build()+`"`) {
		t.Fatalf("%d %v %s", resp.StatusCode, resp.Header, b[:min(len(b), 200)])
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "worker-src 'self'") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "manifest-src 'self'") {
		t.Fatalf("the policy: %s", resp.Header.Get("Content-Security-Policy"))
	}
}

// The worker as tend-server serves it, run in node: see webtest/pwa_test.js.
func TestTheServiceWorkerAndThePlatform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sw.js")
	os.WriteFile(path, swJS(), 0o600)
	runModule(t, "webtest/pwa_test.js", path)
}

// The app installs from the manifest: a standalone app at the root, its colours the default skin's, and icons
// that are what they say.
func TestTheManifestAndItsIcons(t *testing.T) {
	r := newRig(t)
	resp, b := get(t, r.url+"/manifest.webmanifest")
	var m struct {
		Name, Display, Scope string
		StartURL             string `json:"start_url"`
		ThemeColor           string `json:"theme_color"`
		BackgroundColor      string `json:"background_color"`
		Icons                []struct{ Src, Sizes, Type, Purpose string }
	}
	json.Unmarshal(b, &m)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/manifest+json" || m.Name != "tend" || m.Display != "standalone" ||
		m.StartURL != "/?source=pwa" || m.Scope != "/" || m.ThemeColor != iconSkin().Light["bg"] || m.BackgroundColor != m.ThemeColor || len(m.Icons) != 3 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	maskable := false
	for _, ic := range append(m.Icons, struct{ Src, Sizes, Type, Purpose string }{"icon-180.png", "180x180", "image/png", ""}) {
		resp, b := get(t, r.url+"/"+ic.Src)
		img, err := png.Decode(bytes.NewReader(b))
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || err != nil {
			t.Fatalf("%s: %d %v", ic.Src, resp.StatusCode, err)
		}
		if got := fmt.Sprintf("%dx%d", img.Bounds().Dx(), img.Bounds().Dy()); got != ic.Sizes {
			t.Errorf("%s is %s, says %s", ic.Src, got, ic.Sizes)
		}
		_, _, _, corner := img.At(0, 0).RGBA()
		full := ic.Purpose == "maskable" || ic.Src == "icon-180.png"
		if full != (corner == 0xffff) {
			t.Errorf("%s: corner alpha %x", ic.Src, corner)
		}
		maskable = maskable || ic.Purpose == "maskable"
	}
	if !maskable {
		t.Fatal("no maskable icon")
	}
	if resp, _ := get(t, r.url+"/index.html"); resp.StatusCode != 200 {
		t.Fatalf("index.html: %d", resp.StatusCode)
	}
}
