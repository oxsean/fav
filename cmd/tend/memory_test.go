package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/task"
)

func memDir(d *fixture.Dataset, project string) string {
	return filepath.Join(d.Claude, "projects", index.ClaudeProjectName(project), "memory")
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, p, line string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func fileText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func entryNames(es []memory.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

// The webapp project's memories on this machine and on peer, compared and copied both ways through the two homes'
// launchers: nothing there is overwritten, a different text lands in .incoming/ unindexed, Codex's stay read only.
func TestMemoryDiffAndCopyBetweenTwoHomes(t *testing.T) {
	self, peer, _ := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	selfWeb, peerWeb := filepath.Join(self.Work, "webapp"), filepath.Join(peer.Work, "webapp")
	here, there := memDir(self, selfWeb), memDir(peer, peerWeb)

	writeFile(t, filepath.Join(here, "mine.md"), "Only this machine knows.\n")
	appendFile(t, filepath.Join(here, "MEMORY.md"), "- [Mine](mine.md) — only here\n")
	writeFile(t, filepath.Join(there, "theirs.md"), "Only peer knows.\n")
	appendFile(t, filepath.Join(there, "MEMORY.md"), "- [Theirs](theirs.md) — only on peer\n")
	writeFile(t, filepath.Join(there, "deploy.md"), "Peer deploys by hand.\n")
	writeFile(t, filepath.Join(here, "paths.md"), "Build output goes to "+filepath.Join(selfWeb, "dist")+".\n")
	writeFile(t, filepath.Join(there, "paths.md"), "Build output goes to "+filepath.Join(peerWeb, "dist")+".\r\n")

	log, err := journal.Open(journalPath(), func(journal.Envelope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	repos := []task.Repo{{Name: "webapp", Base: "main", Dirs: map[string]string{"local": selfWeb, "peer": peerWeb}}}
	if _, err := log.Append(journal.System, nil, []journal.Event{journal.NewEvent(task.EProjectEdited, task.ProjectEdit{ID: fixture.Project, Repos: &repos})}); err != nil {
		t.Fatal(err)
	}
	log.Close()

	out, err := tendOut(t, "memory", "diff", fixture.Project, "peer", "--json")
	var pairs []remote.MemoryPair
	if err != nil || json.Unmarshal([]byte(out), &pairs) != nil || len(pairs) != 1 {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	c := pairs[0].Diff
	for _, want := range []struct {
		what string
		got  []string
		want []string
	}{
		{"only here", entryNames(c.OnlyHere), []string{"mine.md"}},
		{"only on peer", entryNames(c.OnlyThere), []string{"theirs.md"}},
		{"different", entryNames(c.Differ), []string{"deploy.md"}},
		{"the same", entryNames(c.Same), []string{"oauth-state.md", "paths.md", "release workflow", "webapp OAuth callback"}},
	} {
		if !slices.Equal(want.got, want.want) {
			t.Errorf("%s: %v, want %v", want.what, want.got, want.want)
		}
	}
	for _, e := range c.Same {
		if loose := e.Name == "paths.md" || e.Name == "webapp OAuth callback"; e.Loose != loose {
			t.Errorf("%s: loose %v", e.Name, e.Loose)
		}
	}
	text, err := tendOut(t, "memory", "diff", fixture.Project, "peer")
	if err != nil || !strings.Contains(text, i18n.F("cli.memory.diff_only", "peer", 1)) || !strings.Contains(text, i18n.F("cli.memory.diff_same", 4, 2)) {
		t.Errorf("diff as text: %v\n%s", err, text)
	}

	peerDeploy := fileText(t, filepath.Join(there, "deploy.md"))
	out, err = tendOut(t, "memory", "cp", fixture.Project, "peer", "mine", "deploy.md", "oauth-state", "paths")
	if err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	for _, want := range []string{
		i18n.F("cli.memory.cp_done", "mine.md", "peer", filepath.Join(there, "mine.md")),
		i18n.F("cli.memory.cp_incoming", "deploy.md", "peer", filepath.Join(there, ".incoming", "deploy.md")),
		i18n.F("cli.memory.cp_same", "oauth-state.md", "peer"),
		i18n.F("cli.memory.cp_same_loose", "paths.md", "peer"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cp says %q:\n%s", want, out)
		}
	}
	if fileText(t, filepath.Join(there, "mine.md")) != "Only this machine knows.\n" ||
		fileText(t, filepath.Join(there, "deploy.md")) != peerDeploy ||
		fileText(t, filepath.Join(there, ".incoming", "deploy.md")) != fileText(t, filepath.Join(here, "deploy.md")) {
		t.Error("files on peer")
	}
	if idx := fileText(t, filepath.Join(there, "MEMORY.md")); !strings.HasSuffix(idx, "- [Mine](mine.md) — only here\n") || strings.Count(idx, "(deploy.md)") != 1 {
		t.Errorf("peer's MEMORY.md: %q", idx)
	}

	out, err = tendOut(t, "memory", "cp", fixture.Project, "local", "theirs", "--from", "peer")
	if err != nil || fileText(t, filepath.Join(here, "theirs.md")) != "Only peer knows.\n" ||
		!strings.HasSuffix(fileText(t, filepath.Join(here, "MEMORY.md")), "- [Theirs](theirs.md) — only on peer\n") {
		t.Fatalf("cp from peer to here: %v\n%s", err, out)
	}

	out, err = tendOut(t, "memory", "diff", selfWeb, "peer", "--from", "self", "--dir", peerWeb, "--json")
	pairs = nil
	if err != nil || json.Unmarshal([]byte(out), &pairs) != nil || len(pairs) != 1 || len(pairs[0].Diff.OnlyHere) != 0 || len(pairs[0].Diff.OnlyThere) != 0 {
		t.Errorf("both ends reached as hosts, after the copies: %v\n%s", err, out)
	}

	text, err = tendOut(t, "memory", "diff", selfWeb, "peer", "--dir", peerWeb)
	if hint := i18n.F("cli.memory.diff_hint", shell.User().Join([]string{"tend", "memory", "cp", selfWeb, "peer", "--dir", peerWeb})); err != nil || !strings.Contains(text, hint) {
		t.Errorf("the hint keeps --dir: %v\n%s", err, text)
	}
	if _, err := tendOut(t, "memory", "cp", fixture.Project, "peer", "release workflow"); err == nil {
		t.Error("Codex's memories are not copied")
	}
	if _, err := tendOut(t, "memory", "cp", selfWeb, "peer", "nothing"); err == nil || err.Error() != i18n.F("cli.memory.cp_failed", 1) {
		t.Errorf("a name that is not there: %v", err)
	}
	if _, err := tendOut(t, "memory", "diff", filepath.Join(self.Work, "notes-api"), "peer"); err == nil ||
		err.Error() != i18n.F("cli.memory.no_dir_there", "peer", filepath.Join(self.Work, "notes-api")) {
		t.Errorf("a directory in no project needs --dir: %v", err)
	}
	if _, err := tendOut(t, "memory", "diff", fixture.Project); err == nil {
		t.Error("diff takes a machine")
	}
}
