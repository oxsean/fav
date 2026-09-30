// Command webpreview serves the new Web UI from the working tree on fake data, for looking at it in a browser:
// internal/server/web and internal/server/webtest as they lie on disk (the preview page and its fake server are in
// webtest/preview, the frames it plays in webtest/frames), the preview page at / and the skins at /theme/, all under the
// server's own headers. Nothing reaches a coordinator.
//
//	go run ./tools/webpreview [-addr 127.0.0.1:18765]
//
// The page is at /; ?as=signedout shows the sign-in page, ?as=admin signs in as the admin, #device-<code> a terminal's sign-in, #invite-<code> an
// invitation.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/server"
	"github.com/oxsean/fav/internal/skin"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18765", "the address to listen on")
	flag.Parse()
	root := filepath.Join("internal", "server")
	if _, err := os.Stat(filepath.Join(root, "web", "main.js")); err != nil {
		log.Fatalf("webpreview: run it from the repository's root: %v", err)
	}
	mux := http.NewServeMux()
	serve := func(dir string) http.Handler { return http.FileServer(http.Dir(filepath.Join(root, dir))) }
	mux.Handle("/web/", http.StripPrefix("/web/", serve("web")))
	mux.Handle("/webtest/", http.StripPrefix("/webtest/", serve("webtest")))
	mux.HandleFunc("GET /theme/{file}", func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutSuffix(r.PathValue("file"), ".css")
		sk, err := skin.Named(name)
		if !ok || err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		fmt.Fprint(w, sk.CSS())
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(root, "webtest", "preview", "index.html"))
	})
	log.Printf("webpreview: http://%s/", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.SecureHeaders(w)
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})))
}
