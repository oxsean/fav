//go:build !windows

package capture

// ancestors: POSIX systems always get POSIX quoting.
func ancestors() []string { return []string{"sh"} }
