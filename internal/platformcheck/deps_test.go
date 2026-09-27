package platformcheck

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// serverOnly are what tend-server alone carries: none of them may reach tend, which every machine installs.
var serverOnly = []string{
	"github.com/oxsean/fav/internal/server",
	"github.com/oxsean/fav/internal/auth",
	"github.com/oxsean/fav/internal/tracker",
	"github.com/oxsean/fav/internal/store",
	"modernc.org/sqlite",
}

func TestTendLeavesServerCodeOut(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "./cmd/tend")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	for _, p := range serverOnly {
		if slices.ContainsFunc(deps, func(d string) bool { return d == p || strings.HasPrefix(d, p+"/") }) {
			t.Errorf("./cmd/tend depends on %s: it belongs to tend-server only", p)
		}
	}
	if !slices.Contains(deps, "github.com/oxsean/fav/internal/dial") {
		t.Errorf("./cmd/tend no longer lists internal/dial: the check would pass on a broken build")
	}
}
