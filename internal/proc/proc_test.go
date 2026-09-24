package proc

import "testing"

func TestABatchFileTakesOnlyPlainArguments(t *testing.T) {
	for _, c := range []struct {
		exe  string
		args []string
		bad  bool
	}{
		{`C:\npm\codex.cmd`, []string{"exec", "--json", "-C", `C:\work\a b`}, false},
		{`C:\npm\codex.CMD`, []string{"exec", "-C", `C:\w&calc`}, true},
		{`C:\x\run.bat`, []string{"%PATH%"}, true},
		{`C:\bin\claude.exe`, []string{"a&b"}, false},
		{"/usr/bin/claude", []string{"a|b"}, false},
	} {
		if got := batchUnsafe(c.exe, c.args); got != c.bad {
			t.Errorf("%s %v: %v", c.exe, c.args, got)
		}
	}
}
