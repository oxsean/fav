//go:build unix

package proc

import (
	"os/exec"
	"testing"
	"time"
)

func TestAnExitedChildNotYetReapedIsNotAlive(t *testing.T) {
	c := exec.Command("sh", "-c", "exit 0")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Wait()
	for deadline := time.Now().Add(5 * time.Second); Alive(c.Process.Pid); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a zombie reads as alive")
		}
	}
}
