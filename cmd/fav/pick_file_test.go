package main

import (
	"os"
	"path/filepath"
	"testing"

	fzfui "github.com/oxsean/fav/internal/ui/fzf"
)

func TestPickResultRoundTrip(t *testing.T) {
	t.Setenv("FAV_HOME", t.TempDir())
	f := filepath.Join(t.TempDir(), "pick")
	t.Setenv(fzfui.PickFileEnv, f)
	if err := pickResult("#go status:open"); err != nil {
		t.Fatal(err)
	}
	if out := stdoutOf(t, func() { cmdFzfPick([]string{"read", "old"}) }); out != "#go status:open" {
		t.Errorf("read hands back the picked query: %q", out)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("read removes the file")
	}
	if out := stdoutOf(t, func() { cmdFzfPick([]string{"read", "old", "query"}) }); out != "old query" {
		t.Errorf("no pick keeps the query: %q", out)
	}
}
