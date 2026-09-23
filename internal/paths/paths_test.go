package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func native(p string) string { return filepath.FromSlash(p) }

func TestUnderAndNested(t *testing.T) {
	root := native("/w/app")
	for _, c := range []struct {
		p    string
		want bool
	}{
		{native("/w/app"), true},
		{native("/w/app/internal/a.go"), true},
		{native("/w/app/"), true},
		{"/w/app/sub", true}, // forward slashes, as git prints them on Windows
		{native("/w/apple"), false},
		{native("/w"), false},
		{"", false},
	} {
		if got := Under(c.p, root); got != c.want {
			t.Errorf("Under(%q, %q) = %v", c.p, root, got)
		}
	}
	if !Under(native("/x"), string(filepath.Separator)) {
		t.Error("everything is under the root")
	}
	if !Nested(native("/w"), root) || !Nested(root, native("/w/app/x")) || Nested(root, native("/w/b")) {
		t.Error("Nested: either contains the other")
	}
	if runtime.GOOS == "windows" {
		if !Under(`c:\Users\Me\Work\x`, `C:\users\me\work`) || !Same(`C:/Users/me`, `c:\users\ME\`) {
			t.Error("Windows paths compare without case and with either slash")
		}
		if !Under(`C:\x`, `C:\`) {
			t.Error("a drive root holds its files")
		}
	}
}

func TestInsideAndRebase(t *testing.T) {
	base := native("/w/app")
	if rel, ok := Inside(base, native("/w/app/internal/a.go")); !ok || rel != native("internal/a.go") {
		t.Errorf("Inside: %q %v", rel, ok)
	}
	if rel, ok := Inside(base, base); !ok || rel != "." {
		t.Errorf("Inside itself: %q %v", rel, ok)
	}
	if _, ok := Inside(base, native("/w/..app/x")); ok {
		t.Error("a sibling whose name starts with .. is not inside")
	}
	if got := Rebase(native("/w/app/wt/feat"), base, native("/dev/app")); got != native("/dev/app/wt/feat") {
		t.Errorf("Rebase: %q", got)
	}
	if got := Rebase(base, base, native("/dev/app")); got != native("/dev/app") {
		t.Errorf("Rebase root: %q", got)
	}
}

func TestTildeAndExpand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sep := string(filepath.Separator)
	for in, want := range map[string]string{
		home:                            "~",
		filepath.Join(home, "dev", "x"): "~" + sep + filepath.Join("dev", "x"),
		home + "2" + sep + "x":          home + "2" + sep + "x",
	} {
		if got := Tilde(in); got != want {
			t.Errorf("Tilde(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"~":        home,
		"~/dev/x":  filepath.Join(home, "dev", "x"),
		`~\dev\x`:  filepath.Join(home, "dev", "x"),
		"~other/x": "~other/x",
	} {
		if runtime.GOOS != "windows" && in == `~\dev\x` {
			want = in // a backslash is a file name character on POSIX
		}
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSON(t *testing.T) {
	if got := JSON(`C:\a & b<c>`); got != `C:\\a & b<c>` {
		t.Errorf("JSON: %s", got)
	}
}

func TestExistsAndIsDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	os.WriteFile(f, nil, 0o644)
	if !Exists(f) || IsDir(f) || !IsDir(dir) || Exists(filepath.Join(dir, "nope")) || Exists("") {
		t.Error("Exists / IsDir")
	}
	if !SameFile(f, f) {
		t.Error("SameFile")
	}
}

func TestInTemp(t *testing.T) {
	tmp := os.TempDir()
	if rel, ok := InTemp(filepath.Join(tmp, "claude-501", "x")); !ok || rel != filepath.Join("claude-501", "x") {
		t.Errorf("inside the system temp dir: %q %v", rel, ok)
	}
	root := "/tmp" // macOS's TempDir sits inside /var/folders, so it is inside one
	if runtime.GOOS == "windows" {
		root = tmp
	}
	if _, ok := InTemp(root); ok {
		t.Errorf("%s itself is not inside a temp dir", root)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{"/tmp/x", "/private/tmp/x", "/var/folders/m7/T/x", "/private/var/folders/m7/T/x"} {
			if _, ok := InTemp(p); !ok {
				t.Errorf("%s is a temp path", p)
			}
		}
	}
	if _, ok := InTemp(native("/w/app")); ok {
		t.Error("a project dir is not a temp path")
	}
}
