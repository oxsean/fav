package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/coord"
)

// comingMethods are methods the Web UI calls before the coordinator answers them, each with the card that adds it
// there; a card that does removes its line.
var comingMethods = map[string]string{}

// leavingMethods are methods the Web UI still calls after the coordinator dropped them (it asks has() first), each with
// the card that removes the call; that card removes its line.
var leavingMethods = map[string]string{}

var methodCall = regexp.MustCompile(`\b(?:call|watch|has|send)\(\s*'([a-z_]+(?:\.[a-z_]+)+)'`)

// Every method the Web UI calls, watches or asks hello about is one the coordinator answers (proto.js's methods).
func TestTheWebUICallsTheCoordinatorsMethods(t *testing.T) {
	seen := map[string]bool{}
	for _, dir := range []string{"core", "ui", "pages"} {
		err := filepath.WalkDir(filepath.Join("web", dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, m := range methodCall.FindAllStringSubmatch(string(b), -1) {
				seen[m[1]] = true
				if !slices.Contains(coord.Methods, m[1]) && comingMethods[m[1]] == "" && leavingMethods[m[1]] == "" {
					t.Errorf("%s calls %s: the coordinator has no such method", p, m[1])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for m, card := range comingMethods {
		if slices.Contains(coord.Methods, m) {
			t.Errorf("%s is the coordinator's now (%s): remove it from comingMethods", m, card)
		}
	}
	for m, card := range leavingMethods {
		if !seen[m] || slices.Contains(coord.Methods, m) {
			t.Errorf("the Web UI no longer calls %s, or the coordinator answers it (%s): remove it from leavingMethods", m, card)
		}
	}
	if len(seen) < 20 {
		t.Fatalf("%d methods found", len(seen))
	}
}
