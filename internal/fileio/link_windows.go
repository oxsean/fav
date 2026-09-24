package fileio

import (
	"fmt"
	"os/exec"
	"strings"
)

// LinkDir makes link a name for the directory target: a junction, which needs no privilege (a symlink does).
func LinkDir(target, link string) error {
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
