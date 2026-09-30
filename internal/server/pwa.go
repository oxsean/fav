package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"math"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/oxsean/fav/internal/skin"
)

// ⚠️ Markers the server fills in: the page's build in index.html, and what the service worker caches in sw.js. A test
// holds both files to them.
const (
	buildMeta = `<meta name="tend-build" content="">`
	swBoot    = `const BOOT = {"build":"","files":[]};`
)

func (s *Server) pwaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", indexPage)
	mux.HandleFunc("GET /sw.js", serviceWorker)
	mux.HandleFunc("GET /manifest.webmanifest", manifest)
	for name := range icons {
		mux.HandleFunc("GET /"+name, icon)
	}
}

// indexPage is index.html naming the build of the files it loads: a page the service worker kept tells a newer
// server's hello apart from the start.
var indexHTML = sync.OnceValue(func() []byte {
	b, _ := fs.ReadFile(webFiles, "web/index.html")
	return bytes.Replace(b, []byte(buildMeta), []byte(`<meta name="tend-build" content="`+Build()+`">`), 1)
})

func indexPage(w http.ResponseWriter, r *http.Request) {
	SecureHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(indexHTML())
}

// swFiles are what the service worker keeps for a build: the page ("/") and its scripts and styles.
func swFiles() []string {
	files := []string{"/"}
	fs.WalkDir(webFiles, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "web/sw.js" {
			return err
		}
		if ext := path.Ext(p); ext == ".js" || ext == ".mjs" || ext == ".css" {
			files = append(files, strings.TrimPrefix(p, "web"))
		}
		return nil
	})
	slices.Sort(files[1:])
	return files
}

var swJS = sync.OnceValue(func() []byte {
	b, _ := fs.ReadFile(webFiles, "web/sw.js")
	boot, _ := json.Marshal(map[string]any{"build": Build(), "files": swFiles()})
	return bytes.Replace(b, []byte(swBoot), []byte("const BOOT = "+string(boot)+";"), 1)
})

// serviceWorker is sw.js with its build and files: a new build is a new script, which the browser installs beside
// the running one.
func serviceWorker(w http.ResponseWriter, r *http.Request) {
	SecureHeaders(w)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(swJS())
}

// iconSkin draws the icons and names the installed app's colours: the default skin, since a visitor's own lives in
// their browser.
var iconSkin = sync.OnceValue(func() skin.Skin {
	sk, _ := skin.Named(skin.Presets[0].Name)
	return sk
})

func manifest(w http.ResponseWriter, r *http.Request) {
	bg := iconSkin().Light["bg"]
	m := map[string]any{
		"name": "tend", "short_name": "tend", "id": "/", "start_url": "/?source=pwa", "scope": "/", "display": "standalone",
		"theme_color": bg, "background_color": bg,
		"icons": []map[string]string{
			{"src": "icon-192.png", "sizes": "192x192", "type": "image/png"},
			{"src": "icon-512.png", "sizes": "512x512", "type": "image/png"},
			{"src": "icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	}
	SecureHeaders(w)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/manifest+json")
	json.NewEncoder(w).Encode(m)
}

// icons are the app's icons by file: their size, and whether the mark fills the square (maskable: the system cuts
// its own shape out of it) or sits on a rounded one.
var icons = map[string]struct {
	size int
	full bool
}{
	"icon-180.png": {180, true}, "icon-192.png": {192, false}, "icon-512.png": {512, false}, "icon-maskable-512.png": {512, true},
}

var iconPNG sync.Map

func icon(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	b, ok := iconPNG.Load(name)
	if !ok {
		i := icons[name]
		b, _ = iconPNG.LoadOrStore(name, drawIcon(i.size, i.full))
	}
	SecureHeaders(w)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b.([]byte))
}

// drawIcon draws the brand's ">_" on the accent, as the page's header does, 4×4 samples a pixel. ⚠️ The mark stays
// inside the maskable safe zone, a circle of 40% of the size around the centre.
func drawIcon(size int, full bool) []byte {
	sk := iconSkin()
	accent, mark := rgb(sk.Light["accent"]), rgb(sk.Light["surface"])
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const n = 4
	for y := range size {
		for x := range size {
			var in, on int
			for sy := range n {
				for sx := range n {
					u, v := (float64(x)+(float64(sx)+.5)/n)/float64(size), (float64(y)+(float64(sy)+.5)/n)/float64(size)
					if !full && !inRounded(u, v, .18) {
						continue
					}
					in++
					if onMark(u, v) {
						on++
					}
				}
			}
			if in == 0 {
				continue
			}
			f := float64(on) / float64(in)
			img.SetNRGBA(x, y, color.NRGBA{mix(accent.R, mark.R, f), mix(accent.G, mark.G, f), mix(accent.B, mark.B, f), uint8(255 * in / (n * n))})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func inRounded(u, v, r float64) bool {
	dx, dy := math.Max(r-u, u-(1-r)), math.Max(r-v, v-(1-r))
	return dx <= 0 || dy <= 0 || dx*dx+dy*dy <= r*r
}

// onMark: the chevron's two strokes and the underscore, in the unit square.
func onMark(u, v float64) bool {
	const half = .045
	return segment(u, v, .27, .33, .45, .5) <= half || segment(u, v, .45, .5, .27, .67) <= half || u >= .5 && u <= .73 && v >= .625 && v <= .7
}

func segment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/(dx*dx+dy*dy)))
	return math.Hypot(px-ax-t*dx, py-ay-t*dy)
}

func rgb(hex string) color.NRGBA {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

func mix(a, b uint8, f float64) uint8 { return uint8(math.Round(float64(a)*(1-f) + float64(b)*f)) }
