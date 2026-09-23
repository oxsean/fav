package capture

import (
	"fmt"
	"os/exec"
	"strings"
)

// ⚠️ JXA: Launch Services' handler for the scheme, as "<app path> <bundle id>"; "" when none.
const handlerJXA = `ObjC.import("AppKit")
function run(a) {
	const u = $.NSWorkspace.sharedWorkspace.URLForApplicationToOpenURL($.NSURL.URLWithString(a[0] + "://x"))
	if (u.isNil()) return ""
	const b = $.NSBundle.bundleWithURL(u)
	return u.path.js + " " + (b.isNil() || b.bundleIdentifier.isNil() ? "" : b.bundleIdentifier.js)
}`

func schemeHandler(scheme string) string {
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", handlerJXA, scheme).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func openURL(u string) error {
	if out, err := exec.Command("open", u).CombinedOutput(); err != nil {
		return fmt.Errorf("%s%w", out, err)
	}
	return nil
}
