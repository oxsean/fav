// Package paths holds the path rules that differ by platform: forward slashes from external tools, Windows' case-insensitive
// names and drive roots, the "~" home shorthand, and a path's form inside a JSON string. Compare, nest and shorten paths
// only through here.
package paths

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Clean normalises a path from any source (git prints C:/x on Windows); "" stays "".
func Clean(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(p))
}

func eq(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Same: a and b name the same path (not resolving links).
func Same(a, b string) bool {
	return a != "" && b != "" && eq(Clean(a), Clean(b))
}

// Under: p is dir or inside it.
func Under(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	p, dir = Clean(p), Clean(dir)
	if eq(p, dir) {
		return true
	}
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return len(p) > len(prefix) && eq(p[:len(prefix)], prefix)
}

// Nested: one of a and b is the other or inside it.
func Nested(a, b string) bool { return Under(a, b) || Under(b, a) }

// Inside: p relative to base when p is base or inside it.
func Inside(base, p string) (string, bool) {
	if !Under(p, base) {
		return "", false
	}
	rel, err := filepath.Rel(Clean(base), Clean(p))
	return rel, err == nil
}

// Rebase moves p from under old to under new; p outside old comes back cleaned.
func Rebase(p, old, new string) string {
	rel, ok := Inside(old, p)
	if !ok {
		return Clean(p)
	}
	if rel == "." {
		return Clean(new)
	}
	return filepath.Join(Clean(new), rel)
}

// tempRoots: the system temp directory, and on macOS and Linux the other places scratch files live (/tmp, macOS's
// per-user /var/folders, and the /private paths both resolve to).
func tempRoots() []string {
	roots := []string{os.TempDir()}
	if runtime.GOOS != "windows" {
		roots = append(roots, "/tmp", "/private/tmp", "/var/folders", "/private/var/folders")
	}
	return roots
}

// InTemp: p relative to the temp directory it is inside; a temp directory itself is not inside one.
func InTemp(p string) (string, bool) {
	for _, d := range tempRoots() {
		if rel, ok := Inside(d, p); ok && rel != "." {
			return rel, true
		}
	}
	return "", false
}

// TempEnv points every platform's temp-directory variable (TMPDIR on macOS and Linux, TMP and TEMP on Windows) at dir.
func TempEnv(dir string) []string { return []string{"TMPDIR=" + dir, "TMP=" + dir, "TEMP=" + dir} }

func Exists(p string) bool {
	_, err := os.Stat(p)
	return p != "" && err == nil
}

func IsDir(p string) bool {
	st, err := os.Stat(p)
	return p != "" && err == nil && st.IsDir()
}

// SameFile: two paths of one file (a hard link included).
func SameFile(a, b string) bool {
	if a == b {
		return true
	}
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

// Tilde shortens the home directory to "~".
func Tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, ok := Inside(home, p); ok {
		if rel == "." {
			return "~"
		}
		return "~" + string(filepath.Separator) + rel
	}
	return p
}

// Expand turns "~" and "~/x" (and "~\x" on Windows) into paths under the home directory.
func Expand(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, filepath.FromSlash(p[1:]))
}

// JSON is p as the CLIs write it between the quotes of a JSON string: Windows backslashes doubled, & < > left as they are.
func JSON(p string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(p)
	s := strings.TrimSuffix(b.String(), "\n")
	return s[1 : len(s)-1]
}
