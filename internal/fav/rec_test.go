package fav

import "testing"

func TestBroken(t *testing.T) {
	on := map[string]bool{"/w": true, "/t": true}
	exists := func(p string) bool { return on[p] }
	for _, c := range []struct {
		name            string
		r               Rec
		dirGone, trGone bool
	}{
		{"no paths recorded", Rec{}, false, false},
		{"all present", Rec{Cwd: "/w", TranscriptPath: "/t"}, false, false},
		{"pinned copy survives", Rec{Cwd: "/w", PinnedPath: "/t", TranscriptPath: "/x"}, false, false},
		{"transcript gone", Rec{Cwd: "/w", TranscriptPath: "/x"}, false, true},
		{"dir gone", Rec{Cwd: "/gone", TranscriptPath: "/t"}, true, false},
	} {
		d, g := c.r.Broken(exists)
		if d != c.dirGone || g != c.trGone {
			t.Errorf("%s: got %v %v", c.name, d, g)
		}
	}
}
