//go:build !darwin && !windows

package capture

import (
	"fmt"
	"os/exec"
	"strings"
)

// schemeHandler is the .desktop file xdg-mime assigns to the scheme.
func schemeHandler(scheme string) string {
	out, err := exec.Command("xdg-mime", "query", "default", "x-scheme-handler/"+scheme).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func openURL(u string) error {
	if out, err := exec.Command("xdg-open", u).CombinedOutput(); err != nil {
		return fmt.Errorf("%s%w", out, err)
	}
	return nil
}
