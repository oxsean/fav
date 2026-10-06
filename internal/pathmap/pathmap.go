// Package pathmap carries a path from one machine's file system to another's: a path under one home maps under the
// other's, and Windows and a WSL distro on the same machine share drives (C:\x is /mnt/c/x). Pure strings: both ends'
// rules apply whatever OS tend runs on.
package pathmap

import "strings"

// End is one side of a mapping, as its hello describes it.
type End struct {
	OS   string // GOOS
	Home string // in the end's own form
	Host string // hostname: Windows and WSL share drives only on the same host
	WSL  bool   // a Linux distro under WSL
}

func (e End) windows() bool { return e.OS == "windows" }

func (e End) sep() string {
	if e.windows() {
		return `\`
	}
	return "/"
}

// Map is p (absolute, in from's form) on to; false when to has no counterpart: outside from's home and not a shared
// drive, relative, UNC, "..", or a name to cannot hold.
func Map(p string, from, to End) (string, bool) {
	drive, segs, ok := parse(p, from)
	if !ok {
		return "", false
	}
	if strings.EqualFold(from.Host, to.Host) {
		switch {
		case from.windows() && to.WSL:
			return join("/mnt/"+strings.ToLower(drive), segs, to)
		case from.WSL && to.windows() && len(segs) >= 2 && segs[0] == "mnt" && isLetter(segs[1]):
			return join(strings.ToUpper(segs[1])+`:\`, segs[2:], to)
		}
	}
	hDrive, home, ok := parse(from.Home, from)
	if !ok || to.Home == "" || len(segs) < len(home) || !same(drive, hDrive, from) {
		return "", false
	}
	for i, h := range home {
		if !same(segs[i], h, from) {
			return "", false
		}
	}
	return join(to.Home, segs[len(home):], to)
}

// parse splits an absolute path into its drive ("" off Windows) and names; a UNC path has no counterpart.
func parse(p string, e End) (drive string, segs []string, ok bool) {
	root, segs, ok := split(p, e.OS)
	switch {
	case !ok || strings.HasPrefix(root, `\\`):
		return "", nil, false
	case e.windows():
		return root[:1], segs, true
	}
	return "", segs, true
}

// split splits an absolute path into its root and names by goos's rules: off Windows the root is "/"; on Windows either
// separator, and the root is an upper-case drive ("C:") or a UNC share (\\server\share). False when p is relative,
// drive-relative (C: or C:x), rooted without a drive, or holds "..".
func split(p, goos string) (root string, segs []string, ok bool) {
	e := End{OS: goos}
	rest := p
	if e.windows() {
		rest = strings.ReplaceAll(p, "/", `\`)
		switch {
		case strings.HasPrefix(rest, `\\`):
			server, after, _ := strings.Cut(rest[2:], `\`)
			share, tail, _ := strings.Cut(after, `\`)
			if server == "" || share == "" {
				return "", nil, false
			}
			root, rest = `\\`+server+`\`+share, tail
		case Drive(rest):
			root, rest = strings.ToUpper(rest[:1])+":", rest[2:]
		default:
			return "", nil, false
		}
	} else if strings.HasPrefix(rest, "/") {
		root = "/"
	} else {
		return "", nil, false
	}
	for s := range strings.SplitSeq(rest, e.sep()) {
		switch s {
		case "", ".":
		case "..":
			return "", nil, false
		default:
			segs = append(segs, s)
		}
	}
	return root, segs, true
}

// Under: path is dir or inside it, both absolute on one machine whose OS is goos and read by its rules (Windows: either
// separator, any case, a drive or a UNC share). A path in another machine's form is under nothing: Map carries it over.
func Under(path, dir, goos string) bool {
	pRoot, p, ok := split(path, goos)
	dRoot, d, dOK := split(dir, goos)
	e := End{OS: goos}
	if !ok || !dOK || len(p) < len(d) || !same(pRoot, dRoot, e) {
		return false
	}
	for i := range d {
		if !same(p[i], d[i], e) {
			return false
		}
	}
	return true
}

func join(base string, segs []string, to End) (string, bool) {
	sep := to.sep()
	for _, s := range segs {
		if strings.Contains(s, sep) || to.windows() && strings.ContainsAny(s, `<>:"|?*/`) {
			return "", false
		}
	}
	if len(segs) == 0 {
		return base, true
	}
	if !strings.HasSuffix(base, sep) {
		base += sep
	}
	return base + strings.Join(segs, sep), true
}

func same(a, b string, e End) bool {
	if e.windows() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func isLetter(s string) bool {
	return len(s) == 1 && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z')
}

// Drive: p starts with a Windows drive root (C:\ or C:/).
func Drive(p string) bool {
	return len(p) >= 3 && isLetter(p[:1]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// Abs: p is absolute on some machine, POSIX or Windows.
func Abs(p string) bool { return strings.HasPrefix(p, "/") || Drive(p) }

// Base is p's last element with either separator.
func Base(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
