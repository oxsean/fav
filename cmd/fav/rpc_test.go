package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

func fixtureMachine(t *testing.T) *fixture.Dataset {
	t.Helper()
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAV_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	return d
}

// rpcLines runs fav with args, stdin holding in, and returns what it wrote to stdout, line by line.
func rpcLines(t *testing.T, in string, args ...string) []string {
	t.Helper()
	dir := t.TempDir()
	stdinPath, stdoutPath := filepath.Join(dir, "in"), filepath.Join(dir, "out")
	os.WriteFile(stdinPath, []byte(in), 0o600)
	stdin, _ := os.Open(stdinPath)
	stdout, _ := os.Create(stdoutPath)
	defer stdin.Close()
	defer stdout.Close()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdin, stdout
	err := run(args)
	os.Stdin, os.Stdout = oldIn, oldOut
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(stdoutPath)
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func TestRpcAnswersOnStdoutOnly(t *testing.T) {
	d := fixtureMachine(t)
	oauth := d.Get("oauth")
	msgs, _ := json.Marshal(wire.Frame{Kind: wire.KindReq, ID: 2, Method: remote.MMessages,
		Params: json.RawMessage(`{"provider":"` + oauth.Provider + `","session_id":"` + oauth.ID + `","before":-1,"n":3}`)})
	list := `{"kind":"req","id":1,"method":"list"}` + "\n"

	all := rpcLines(t, list+"not json\n"+string(msgs)+"\n", "rpc", "--stdio")
	if len(all) != 2 {
		t.Fatalf("every request is answered, noise is skipped: %q", all)
	}
	got := map[int64]wire.Frame{}
	for _, line := range all {
		var f wire.Frame
		if json.Unmarshal([]byte(line), &f) != nil || f.Kind != wire.KindRes || !f.OK {
			t.Fatalf("%.300s", line)
		}
		got[f.ID] = f
	}
	var l remote.List
	if json.Unmarshal(got[1].Result, &l) != nil || len(l.Sessions) == 0 {
		t.Errorf("list: %.300s", got[1].Result)
	}
	var page struct{ Msgs []json.RawMessage }
	if json.Unmarshal(got[2].Result, &page) != nil || len(page.Msgs) != 3 {
		t.Errorf("messages: %.300s", got[2].Result)
	}
}

func TestHostsCheck(t *testing.T) {
	fixtureMachine(t)
	dial := func(h fav.Host) (*remote.Client, error) {
		if h.Name == "gone" {
			return nil, &wire.Error{Code: wire.CodeOffline, Detail: "ssh: connect to host gone port 22: Operation timed out\nmore"}
		}
		return remote.Pipe(remote.NewLocal("test")), nil
	}
	reports := checkHosts([]fav.Host{{Name: "here"}, {Name: "gone", SSH: "gone"}}, dial, "", 20*time.Second)
	here, gone := reports[0], reports[1]
	if len(here.fails) != 0 {
		t.Fatalf("here: %v", here.fails)
	}
	for _, row := range probeRows {
		if here.cells[row] == "" {
			t.Errorf("here: no %s cell", row)
		}
	}
	if !strings.Contains(here.cells["cli.hosts.row_page"], "·") {
		t.Errorf("page: %q", here.cells["cli.hosts.row_page"])
	}
	if len(gone.fails) != 1 || !strings.Contains(gone.fails[0], "Operation timed out") || strings.Contains(gone.fails[0], "more") {
		t.Errorf("gone: the reason and the first line of the detail: %v", gone.fails)
	}
}
