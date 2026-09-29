package agent

import (
	"os"
	"testing"

	"github.com/oxsean/fav/internal/shell"
)

func TestOnlyARunsOwnReportsGoWithoutApproval(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	q := shell.POSIX.Quote(self)
	zsh := func(script string) string { return "/bin/zsh -lc " + shell.POSIX.Quote(script) }
	for cmd, want := range map[string]bool{
		q + " run verdict pass 'looks good'":                      true,
		zsh(q + " run verdict pass 'looks good'"):                 true,
		"/bin/bash -c " + shell.POSIX.Quote(q+` run note "half"`): true,
		q + " run ask 'which db?'":                                true,
		q + " run note --pr https://x.example/pr/1":               true,
		q + " run plan /tmp/plan.json":                            true,
		q + " run plan - <<'PLAN'\n{\"tasks\":[]}\nPLAN":          true,
		zsh(q + " run plan - <<'PLAN'\n{\"tasks\":[]}\nPLAN\n"):   true,
		zsh(q + " run plan - <<\"EOF\"\n{\"a\":\"$HOME\"}\nEOF"):  true,
		q + " run plan - <<'PLAN'\n{}\n  PLAN":                    false,
		q + " run note x; rm -rf ~":                               false,
		q + " run note $(id)":                                     false,
		q + " run continue r_1 hi":                                false,
		q + " sessions":                                           false,
		q + " run":                                                false,
		"tend run note x":                                         self == "tend",
		zsh("cd /; " + q + " run note x"):                         false,
		zsh(q+" run note a") + " extra":                           false,
		q + " run plan - <<PLAN\n{}\nPLAN":                        false,
		q + " run plan - <<'PLAN'\n{}\nPLAN\nrm -rf ~":            false,
		q + " run plan - <<'PLAN' && rm x\n{}\nPLAN":              false,
		q + " run plan - <<'PLAN'\n{}\n":                          false,
		q + " run note <<'PLAN'\nx\nPLAN":                         false,
		q + " run plan x - <<'PLAN'\n{}\nPLAN":                    false,
	} {
		if got := OwnReport(cmd, self); got != want {
			t.Errorf("OwnReport(%q) = %v", cmd, got)
		}
	}
}

// A log read away from its node knows the run's tend by its name only.
func TestAnyTendCountsWithoutTheNodesPath(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/home/u/.local/bin/tend run note x":                true,
		`'C:\Users\u\.local\bin\tend.exe' run verdict pass`: true,
		"/bin/zsh -lc '/opt/tend run ask \"which?\"'":       true,
		"/usr/bin/pretend run note x":                       false,
		"tend sessions":                                     false,
	} {
		if got := OwnReport(cmd, ""); got != want {
			t.Errorf("OwnReport(%q) = %v", cmd, got)
		}
	}
}
