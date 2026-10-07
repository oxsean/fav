package task

import (
	"net/url"
	"strings"
)

// RemoteKey is a git remote's URL as host/path, so the ssh, scp-like and https forms of one repository compare equal:
// the host in lower case, without user, password, port, a trailing slash or ".git". A local path or file:// URL keeps
// its path; "" when url names no repository.
func RemoteKey(remote string) string {
	s := strings.TrimSpace(remote)
	var host, path string
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		host, path = u.Hostname(), u.Path
		if u.Scheme == "file" {
			host = ""
		}
	case scpLike(s):
		at, rest, _ := strings.Cut(s, ":")
		if i := strings.LastIndex(at, "@"); i >= 0 {
			at = at[i+1:]
		}
		host, path = at, rest
	default:
		path = s
	}
	path = strings.TrimSuffix(strings.TrimRight(path, `/\`), ".git")
	if host == "" {
		if path == "" || strings.Contains(remote, "://") && !strings.HasPrefix(path, "/") {
			return ""
		}
		return path
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	return strings.ToLower(host) + "/" + path
}

// scpLike: user@host:path, the colon before any slash, and not a Windows drive (C:\x).
func scpLike(s string) bool {
	i := strings.Index(s, ":")
	return i > 1 && !strings.ContainsAny(s[:i], `/\`)
}
