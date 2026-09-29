package render

import (
	"testing"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
)

func TestWaitKindTellsAGoodEndFromAnError(t *testing.T) {
	zero, one := 0, 1
	if got := WaitKind(&task.Run{State: task.Exited, ExitCode: &zero}); got != i18n.T("sit.ended") {
		t.Fatalf("exit 0: %q", got)
	}
	if got := WaitKind(&task.Run{State: task.Exited, ExitCode: &one}); got != i18n.T("sit.exited") {
		t.Fatalf("exit 1: %q", got)
	}
}
