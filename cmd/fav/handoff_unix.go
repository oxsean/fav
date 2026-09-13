//go:build !windows

package main

import (
	"os"
	"syscall"
)

func handOff(bin string, argv []string) error { return syscall.Exec(bin, argv, os.Environ()) }
