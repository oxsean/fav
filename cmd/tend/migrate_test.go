package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/testkit"
)

// claudeSession writes a Claude session sid in cwd on d, with three turns.
func claudeSession(t *testing.T, d *fixture.Dataset, sid, cwd string) string {
	t.Helper()
	l := fixture.NewLiveClaude(d.Claude, sid, cwd, "cli")
	for _, s := range []string{"one", "two", "three"} {
		if l.User("ask "+s) != nil || l.Reply("answer "+s) != nil {
			t.Fatal("transcript")
		}
	}
	return l.Path()
}

// goOn appends a turn to a transcript, as a resume would.
func goOn(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"type":"user","message":{"role":"user","content":"` + text + `"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
}

// migrateOut runs tend migrate in this process: stdout, stderr and the error.
func migrateOut(t *testing.T, args ...string) (out, errText string, err error) {
	t.Helper()
	errText = stderrOf(t, func() { out, err = tendOut(t, append([]string{"migrate"}, args...)...) })
	return out, errText, err
}

func copyAt(d *fixture.Dataset, dir, sid string) string {
	return filepath.Join(d.Claude, "projects", index.ClaudeProjectName(dir), sid+".jsonl")
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// A session on self migrated to peer, through tend migrate and the peer's node over its launcher: copied with its cwd
// carried over, resumed there with its note, refused while nothing changed, fast-forwarded after self went on,
// refused once peer went on (tend show says so), migrated back, and refused once both went on.
func TestMigrateBetweenTwoHomes(t *testing.T) {
	self, peer, launcher := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	const sid = "5e551011-0c1a-4de0-8000-0000000000b1"
	here, there := filepath.Join(self.Work, "webapp"), filepath.Join(peer.Work, "webapp")
	orig := claudeSession(t, self, sid, here)
	if _, err := tendOut(t, "sessions"); err != nil {
		t.Fatal(err)
	}

	if _, errText, err := migrateOut(t, sid, "--to", "peer", "--dir", there, "--dry-run"); err != nil ||
		!strings.Contains(errText, i18n.F("cli.migrate.files", 1, render.Bytes(fileSize(t, orig)))) || exists(copyAt(peer, there, sid)) {
		t.Fatalf("dry run: %v\n%s", err, errText)
	}
	out, errText, err := migrateOut(t, sid, "--to", "peer", "--dir", there)
	if err != nil || strings.TrimSpace(out) != "tend resume peer:"+sid {
		t.Fatalf("migrate: %q %v\n%s", out, err, errText)
	}
	copied, err := os.ReadFile(copyAt(peer, there, sid))
	if err != nil || !strings.Contains(string(copied), testkit.JSONString(there)) || strings.Contains(string(copied), testkit.JSONString(here)) {
		t.Fatalf("on peer, in its directory there: %v\n%s", err, copied)
	}

	if runtime.GOOS != "windows" {
		log, stubs := filepath.Join(t.TempDir(), "claude.log"), filepath.Join(peer.Root, "stubs")
		os.Remove(filepath.Join(stubs, "claude")) // ⚠️ a link to this test binary: writing through it overwrites the binary
		os.WriteFile(filepath.Join(stubs, "claude"), []byte("#!/bin/sh\npwd > "+log+"\nprintf '%s\\n' \"$@\" >> "+log+"\nexit 1\n"), 0o755)
		exec.Command(launcher(peer), "resume", sid, "--terminal", "--no-herdr").CombinedOutput()
		testkit.LinkCLIs(t, stubs, map[string]string{"claude": ""})
		got, _ := os.ReadFile(log)
		lines := strings.Split(strings.TrimSpace(string(got)), "\n")
		if len(lines) < 4 || lines[0] != there || lines[1] != "--resume" || lines[2] != sid || !strings.Contains(lines[len(lines)-1], filepath.Join(peer.Home, "migrations")) {
			t.Fatalf("claude on peer starts in the new directory, on the session, with its note first:\n%s", got)
		}
	}

	if _, _, err := migrateOut(t, sid, "--to", "peer", "--dir", there); err == nil || refusal(err) != remote.RefusedSame {
		t.Fatalf("again, nothing changed: %v", err)
	}
	goOn(t, orig, "went on here")
	if _, errText, err := migrateOut(t, sid, "--to", "peer", "--dir", there); err != nil {
		t.Fatalf("fast-forward: %v\n%s", err, errText)
	}
	if b, _ := os.ReadFile(copyAt(peer, there, sid)); !strings.Contains(string(b), "went on here") {
		t.Fatalf("peer has what self went on with:\n%s", b)
	}

	goOn(t, copyAt(peer, there, sid), "went on there")
	if out, err := tendOut(t, "show", sid); err != nil || !strings.Contains(out, i18n.F("remote.copies.there", "peer")) {
		t.Errorf("tend show tells where it went on: %v\n%s", err, out)
	}
	if _, _, err := migrateOut(t, sid, "--to", "peer", "--dir", there); refusal(err) != remote.RefusedThere {
		t.Fatalf("peer went on: %v", err)
	}
	if _, errText, err := migrateOut(t, "peer:"+sid, "--to", "self", "--dir", here); err != nil {
		t.Fatalf("back: %v\n%s", err, errText)
	}
	if b, _ := os.ReadFile(orig); !strings.Contains(string(b), "went on there") || strings.Contains(string(b), testkit.JSONString(there)) {
		t.Fatalf("back home, in its own directory:\n%s", b)
	}
	if out, err := tendOut(t, "show", sid); err != nil || !strings.Contains(out, i18n.T("remote.copies.superseded")) ||
		strings.Contains(out, i18n.T("remote.copies.diverged")) {
		t.Errorf("tend show judges the migration back, the earlier ones superseded: %v\n%s", err, out)
	}
	goOn(t, orig, "here again")
	goOn(t, copyAt(peer, there, sid), "and there again")
	if _, _, err := migrateOut(t, sid, "--to", "peer", "--dir", there); refusal(err) != remote.RefusedDiverged {
		t.Fatalf("forked: %v", err)
	}

	oauth := self.Get("oauth")
	if _, _, err := migrateOut(t, oauth.ID, "--to", "peer", "--dir", there); refusal(err) != remote.RefusedExists {
		t.Fatalf("an unrelated session with the same id on peer: %v", err)
	}
}

// A migration that crashed at a cut point on the target, or on the initiator after the commit, is finished by the
// next tend migrate; --move puts the original in the trash once the target committed.
func TestMigrateResumesAfterACrashAndMoves(t *testing.T) {
	self, peer, launcher := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	here, there := filepath.Join(self.Work, "webapp"), filepath.Join(peer.Work, "webapp")
	for i, point := range []string{"migrate.half", "migrate.placing", "migrate.committed"} {
		sid := "5e551011-0c1a-4de0-8000-0000000000c" + string(rune('1'+i))
		goOn(t, claudeSession(t, self, sid, here), strings.Repeat("x", 3<<19)) // past one chunk: half is reached
		if _, err := tendOut(t, "sessions"); err != nil {
			t.Fatal(err)
		}
		c := exec.Command(launcher(self), "migrate", sid, "--to", "peer", "--dir", there)
		c.Env = append(os.Environ(), proc.EnvCrashAt+"="+point)
		out, err := c.CombinedOutput()
		t.Logf("%s:\n%s", point, out)
		if err == nil || point == "migrate.committed" && exitCode(err) != 86 {
			t.Fatalf("%s: the run stops there: %v\n%s", point, err, out)
		}
		stdout, errText, err := migrateOut(t, sid, "--to", "peer", "--dir", there)
		if err != nil || !strings.Contains(errText, strings.TrimSpace(i18n.F("cli.migrate.resumed", ""))) || strings.TrimSpace(stdout) != "tend resume peer:"+sid {
			t.Fatalf("%s: goes on: %q %v\n%s", point, stdout, err, errText)
		}
		if !exists(copyAt(peer, there, sid)) {
			t.Fatalf("%s: on peer", point)
		}
	}

	const sid = "5e551011-0c1a-4de0-8000-0000000000d1"
	orig := claudeSession(t, self, sid, here)
	if _, err := tendOut(t, "sessions"); err != nil {
		t.Fatal(err)
	}
	if _, errText, err := migrateOut(t, sid, "--to", "peer", "--dir", there, "--move"); err != nil || exists(orig) || !exists(copyAt(peer, there, sid)) {
		t.Fatalf("moved: %v\n%s", err, errText)
	}
	if out, err := tendOut(t, "trash"); err != nil || !strings.Contains(out, sid[:8]) {
		t.Errorf("the original waits in the trash: %v\n%s", err, out)
	}
}

// A migration maps the cwd values in each of the session's project's repositories to that repository's directory
// there, as the project records both machines' directories: a turn the session took in another repository of its
// project lands in that repository's checkout on peer, not where the home mapping would put it, and the note lists
// each pair once.
func TestMigrateMapsTheProjectsDirectories(t *testing.T) {
	self, peer, _ := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	const sid = "5e551011-0c1a-4de0-8000-0000000000e2"
	here, there := filepath.Join(self.Work, "webapp"), filepath.Join(peer.Work, "webapp")
	api, apiThere := filepath.Join(self.Work, "notes-api"), filepath.Join(peer.Root, "clones", "notes-api-there")
	orig := claudeSession(t, self, sid, here)
	appendLine(t, orig, `{"type":"user","cwd":`+testkit.JSONString(filepath.Join(api, "cmd"))+`,"message":{"role":"user","content":"over in the api"}}`)
	log, err := journal.Open(journalPath(), func(journal.Envelope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	repos := []task.Repo{{Name: "webapp", Base: "main", Dirs: map[string]string{"local": here, "peer": there}},
		{Name: "notes-api", Base: "main", Dirs: map[string]string{"local": api, "peer": apiThere}}}
	if _, err := log.Append(journal.System, nil, []journal.Event{journal.NewEvent(task.EProjectEdited, task.ProjectEdit{ID: fixture.Project, Repos: &repos})}); err != nil {
		t.Fatal(err)
	}
	log.Close()
	if _, err := tendOut(t, "sessions"); err != nil {
		t.Fatal(err)
	}

	if out, errText, err := migrateOut(t, sid, "--to", "peer"); err != nil || strings.TrimSpace(out) != "tend resume peer:"+sid {
		t.Fatalf("migrate into the project's directory there: %q %v\n%s", out, err, errText)
	}
	copied, err := os.ReadFile(copyAt(peer, there, sid))
	if err != nil || !strings.Contains(string(copied), testkit.JSONString(filepath.Join(apiThere, "cmd"))) {
		t.Fatalf("the api turn is in the api's checkout on peer: %v\n%s", err, copied)
	}
	notes, _ := filepath.Glob(filepath.Join(peer.Home, "migrations", "*", "note.md"))
	if len(notes) != 1 {
		t.Fatalf("one note: %v", notes)
	}
	note, _ := os.ReadFile(notes[0])
	for _, pair := range []string{here + " -> " + there, api + " -> " + apiThere} {
		if strings.Count(string(note), pair) != 1 {
			t.Errorf("the note lists %s once:\n%s", pair, note)
		}
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// The initiator crashed once the target committed, the session went on here: the next tend migrate finishes that
// commit and says the rest needs another run, which carries it over.
func TestMigrateSaysWhenItFinishedAnEarlierRun(t *testing.T) {
	self, peer, launcher := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	const sid = "5e551011-0c1a-4de0-8000-0000000000e4"
	here, there := filepath.Join(self.Work, "webapp"), filepath.Join(peer.Work, "webapp")
	orig := claudeSession(t, self, sid, here)
	if _, err := tendOut(t, "sessions"); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(launcher(self), "migrate", sid, "--to", "peer", "--dir", there)
	c.Env = append(os.Environ(), proc.EnvCrashAt+"=migrate.committed")
	if out, err := c.CombinedOutput(); exitCode(err) != 86 {
		t.Fatalf("stops after the commit: %v\n%s", err, out)
	}
	goOn(t, orig, "went on here meanwhile")
	if _, errText, err := migrateOut(t, sid, "--to", "peer", "--dir", there); err != nil || !strings.Contains(errText, i18n.F("cli.migrate.behind", "peer")) {
		t.Fatalf("finished, behind: %v\n%s", err, errText)
	}
	if _, errText, err := migrateOut(t, sid, "--to", "peer", "--dir", there); err != nil {
		t.Fatalf("the rest: %v\n%s", err, errText)
	}
	if b, _ := os.ReadFile(copyAt(peer, there, sid)); !strings.Contains(string(b), "went on here meanwhile") {
		t.Fatalf("carried over:\n%s", b)
	}
}

func refusal(err error) string {
	var me *remote.MigrateError
	if errors.As(err, &me) {
		return me.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}
