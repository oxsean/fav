//go:build !windows

package shell

// ancestors: POSIX systems always get POSIX quoting.
func ancestors() []string { return []string{"sh"} }
