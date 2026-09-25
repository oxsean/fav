//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
)

// Windows has no exec: run the child on this terminal and pass its exit code back.
func handOff(bin string, argv []string) error {
	c := exec.Command(bin, argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	return err
}
