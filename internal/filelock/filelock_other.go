//go:build !unix && !windows

package filelock

import "os"

const probeMode = os.O_RDONLY

func lockFile(*os.File, bool) (func(), error) { return func() {}, nil }
