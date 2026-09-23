//go:build !unix && !windows

package main

import "os/exec"

func detach(*exec.Cmd) {}
