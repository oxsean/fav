package herdr

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLiveCreateTabAndRun(t *testing.T) {
	if os.Getenv("HERDR_LIVE") == "" || !Active() {
		t.Skip("需要 HERDR_LIVE=1 且运行在 Herdr 中")
	}
	wsID := os.Getenv("HERDR_WORKSPACE_ID")
	pane, err := CreateTab(wsID, "/tmp", "fav-selftest")
	if err != nil {
		t.Fatalf("CreateTab: %v", err)
	}
	t.Logf("新 pane: %s (tab %s)", pane.PaneID, pane.TabID)
	defer func() {
		if err := CloseTab(pane.TabID); err != nil {
			t.Errorf("清理失败，请手动关掉 tab %s: %v", pane.TabID, err)
		}
	}()

	if err := Run(pane.PaneID, "echo fav-selftest-marker"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	raw, err := exec.Command("herdr", "pane", "read", pane.PaneID, "--format", "text").Output()
	if err != nil {
		t.Fatalf("pane read: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "fav-selftest-marker") {
		t.Fatalf("没在新 pane 里看到命令输出，实际读到：%q", got)
	}
	t.Log("命令确实在新 tab 的 pane 里执行了")
}
