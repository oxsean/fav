package proc

import "os"

// EnvCrashAt names a cut point where the process exits at once, as if killed: the run supervisor's (internal/node)
// and a migration's (internal/migrate, internal/remote; named migrate.*). Tests only.
const EnvCrashAt = "TEND_CRASH_AT"

// CrashAt exits the process at once with status 86 when EnvCrashAt names point.
func CrashAt(point string) {
	if os.Getenv(EnvCrashAt) == point {
		os.Exit(86)
	}
}
