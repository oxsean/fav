package memory

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// homes points the agents and tend at fresh directories.
func homes(t *testing.T) (claude, codex string) {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	claude, codex = filepath.Join(root, "claude"), filepath.Join(root, "codex")
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("TEND_HOME", filepath.Join(root, "tend"))
	return claude, codex
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestClaudeDirFollowsTheRepository(t *testing.T) {
	homes(t)
	work := t.TempDir()
	if r, err := filepath.EvalSymlinks(work); err == nil {
		work = r
	}
	plain := filepath.Join(work, "plain")
	os.MkdirAll(plain, 0o755)
	if got, want := ClaudeDir(plain), filepath.Join(index.ClaudeProjectDir(plain), "memory"); got != want {
		t.Errorf("outside git: %s, want %s", got, want)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := filepath.Join(work, "repo")
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "side", filepath.Join(work, "repo-side"))
	want := filepath.Join(index.ClaudeProjectDir(repo), "memory")
	for _, dir := range []string{repo, filepath.Join(repo, "src"), filepath.Join(work, "repo-side")} {
		if got := ClaudeDir(dir); got != want {
			t.Errorf("%s: %s, want the main checkout's %s", dir, got, want)
		}
	}
}

func TestClaudeDirTakesAutoMemoryDirectory(t *testing.T) {
	claude, _ := homes(t)
	moved := filepath.Join(t.TempDir(), "mem")
	write(t, filepath.Join(claude, "settings.json"), `{"theme":"dark","autoMemoryDirectory":`+testkit.JSONString(moved)+`}`)
	if got := ClaudeDir(t.TempDir()); got != moved {
		t.Errorf("ClaudeDir = %s, want autoMemoryDirectory %s", got, moved)
	}
}

const index1 = "# Memory\n\n- [Deploy steps](deploy.md) — how the staging deploy runs\n- [Old note](gone.md) - a file no longer there\n- [Bare link](bare.md)\n"

func TestLoadReadsTheIndexAndFrontMatter(t *testing.T) {
	homes(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "MEMORY.md"), index1)
	write(t, filepath.Join(dir, "deploy.md"), "---\nname: deploy-steps\ndescription: \"Staging deploy, step by step\"\nmetadata:\n  type: project\n---\n\nRun it.\n")
	write(t, filepath.Join(dir, "bare.md"), "no front matter\r\nat all\r\n")
	write(t, filepath.Join(dir, "loose.md"), "---\nname: loose\n---\nnot in the index\n")
	write(t, filepath.Join(dir, ".incoming", "deploy.md"), "the other side's\n")
	write(t, filepath.Join(dir, "notes.txt"), "not a memory\n")

	s, ok := Load(KindClaude, dir)
	if !ok {
		t.Fatal("Load: no set")
	}
	if s.Index != filepath.Join(dir, "MEMORY.md") || s.Lines != 5 || s.Bytes != int64(len(index1)) || s.Over {
		t.Errorf("set: %+v", s)
	}
	byName := map[string]Item{}
	for _, it := range s.Items {
		byName[filepath.Base(it.File)] = it
	}
	if len(byName) != 3 {
		t.Fatalf("items: %+v", s.Items)
	}
	if d := byName["deploy.md"]; d.Title != "deploy-steps" || d.Description != "Staging deploy, step by step" || !d.InIndex || d.Size == 0 || d.At.IsZero() {
		t.Errorf("front matter wins: %+v", d)
	}
	if b := byName["bare.md"]; b.Title != "Bare link" || b.Description != "" || !b.InIndex {
		t.Errorf("index line fills in: %+v", b)
	}
	if l := byName["loose.md"]; l.Title != "loose" || l.InIndex {
		t.Errorf("not indexed: %+v", l)
	}
	write(t, filepath.Join(dir, "bare-lf.md"), "no front matter\nat all\n")
	again, _ := Load(KindClaude, dir)
	var crlf, lf Item
	for _, it := range again.Items {
		switch filepath.Base(it.File) {
		case "bare.md":
			crlf = it
		case "bare-lf.md":
			lf = it
		}
	}
	if crlf.SHA == lf.SHA || crlf.Norm != lf.Norm || lf.SHA != lf.Norm {
		t.Errorf("sha keeps line ends, norm does not: crlf %+v lf %+v", crlf, lf)
	}
}

func TestLoadDescriptionFromTheIndexDash(t *testing.T) {
	homes(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "MEMORY.md"), index1)
	write(t, filepath.Join(dir, "deploy.md"), "plain\n")
	s, _ := Load(KindClaude, dir)
	if len(s.Items) != 1 || s.Items[0].Title != "Deploy steps" || s.Items[0].Description != "how the staging deploy runs" {
		t.Errorf("items: %+v", s.Items)
	}
}

func TestOverTheLoadLimit(t *testing.T) {
	homes(t)
	for _, c := range []struct {
		name string
		body string
		over bool
	}{
		{"200 lines", strings.Repeat("- [a](a.md)\n", 200), false},
		{"201 lines", strings.Repeat("- [a](a.md)\n", 201), true},
		{"25 KB", strings.Repeat("x", 25<<10) + "\n", true},
		{"short", "- [a](a.md)\n", false},
	} {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "MEMORY.md"), c.body)
		if s, _ := Load(KindClaude, dir); s.Over != c.over {
			t.Errorf("%s: over %v, want %v", c.name, s.Over, c.over)
		}
	}
}

func TestLoadMissingDirectory(t *testing.T) {
	homes(t)
	if _, ok := Load(KindClaude, filepath.Join(t.TempDir(), "nothing")); ok {
		t.Error("a missing directory is no set")
	}
}

func globalFixture(dir, other string) string {
	return "# Task Group: webapp OAuth fixes\nscope: login callback and state handling\napplies_to: cwd=" + filepath.Join(dir, "src") +
		"; reuse_rule=safe while the callback is unchanged\n\n## Task 1: state decoded twice, fixed\n\n" +
		"# Task Group: another checkout\nscope: unrelated\napplies_to: cwd=" + other + "; reuse_rule=checkout-specific\n\n" +
		"# Task Group: release workflow\nscope: how releases are cut\napplies_to: cwd=any repository using the release script; reuse_rule=general\n"
}

func TestListCodexGlobalByAppliesTo(t *testing.T) {
	_, codex := homes(t)
	dir, other := t.TempDir(), t.TempDir()
	write(t, filepath.Join(codex, "memories", "MEMORY.md"), globalFixture(dir, other))
	sets := List([]string{dir}, true)
	var mine, loose *Set
	for i := range sets {
		switch {
		case sets[i].Kind == KindCodexGlobal && sets[i].Dir == dir:
			mine = &sets[i]
		case sets[i].Kind == KindCodexGlobal && sets[i].Dir == "":
			loose = &sets[i]
		}
	}
	if mine == nil || len(mine.Items) != 1 || mine.Items[0].Title != "webapp OAuth fixes" ||
		mine.Items[0].Description != "login callback and state handling" || mine.Items[0].Line != 1 ||
		mine.Items[0].File != filepath.Join(codex, "memories", "MEMORY.md") {
		t.Errorf("blocks applying to the directory: %+v", mine)
	}
	if loose == nil || len(loose.Items) != 1 || loose.Items[0].Title != "release workflow" || loose.Items[0].Line != 11 {
		t.Errorf("blocks with no directory are kept apart: %+v", loose)
	}
	if mine.Over || loose.Over {
		t.Error("Claude's load limit is not Codex's")
	}
	if got := List([]string{dir}, false); slices.ContainsFunc(got, func(s Set) bool { return s.Kind == KindCodexGlobal }) {
		t.Error("global only when asked")
	}
}

func TestListClaudeSetsOncePerMemoryDirectory(t *testing.T) {
	homes(t)
	dir := t.TempDir()
	write(t, filepath.Join(ClaudeDir(dir), "MEMORY.md"), "- [a](a.md) — first\n")
	write(t, filepath.Join(ClaudeDir(dir), "a.md"), "a\n")
	sets := List([]string{dir, dir, t.TempDir()}, false)
	if len(sets) != 1 || sets[0].Kind != KindClaude || sets[0].Dir != ClaudeDir(dir) || len(sets[0].Items) != 1 {
		t.Errorf("sets: %+v", sets)
	}
}

func TestReadOnlyUnderTheMemoryRoots(t *testing.T) {
	claude, codex := homes(t)
	dir := t.TempDir()
	mem := ClaudeDir(dir)
	write(t, filepath.Join(mem, "a.md"), "alpha\n")
	write(t, filepath.Join(codex, "memories", "MEMORY.md"), "# Task Group: g\n")
	write(t, filepath.Join(claude, "settings.json"), `{}`)
	write(t, filepath.Join(claude, "projects", "x", "s1.jsonl"), "{}\n")
	text, at, sha, err := Read(filepath.Join(mem, "a.md"))
	if err != nil || text != "alpha\n" || at.IsZero() || sha == "" {
		t.Errorf("Read: %q %v %q %v", text, at, sha, err)
	}
	if _, _, _, err := Read(filepath.Join(codex, "memories", "MEMORY.md")); err != nil {
		t.Errorf("Codex memories: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	write(t, outside, "secret\n")
	link := filepath.Join(mem, "link.md")
	linked := os.Symlink(outside, link) == nil
	for _, p := range []string{filepath.Join(claude, "settings.json"), filepath.Join(claude, "projects", "x", "s1.jsonl"),
		filepath.Join(mem, "..", "..", "..", "settings.json"), outside, "relative.md", mem} {
		if _, _, _, err := Read(p); !errors.Is(err, ErrOutside) {
			t.Errorf("Read(%s) = %v, want ErrOutside", p, err)
		}
	}
	if linked {
		if _, _, _, err := Read(link); !errors.Is(err, ErrOutside) {
			t.Errorf("a link out of the root: %v", err)
		}
	}
	if _, _, _, err := Read(filepath.Join(mem, "missing.md")); !errors.Is(err, ErrOutside) {
		t.Errorf("a missing file answers as one outside: %v", err)
	}
}

func TestTrashTakesTheIndexLineAndRestorePutsItBack(t *testing.T) {
	homes(t)
	mem := ClaudeDir(t.TempDir())
	write(t, filepath.Join(mem, "MEMORY.md"), "- [A](a.md) — first\n- [B](b.md) — second\n")
	write(t, filepath.Join(mem, "a.md"), "alpha\n")
	write(t, filepath.Join(mem, "b.md"), "beta\n")
	session := tend.TrashEntry{Provider: tend.ProviderClaude, SessionID: "s1", Title: "a session"}
	sessionFile := filepath.Join(t.TempDir(), "s1.jsonl")
	write(t, sessionFile, "{}\n")
	if _, err := tend.MoveToTrash(session, []string{sessionFile}); err != nil {
		t.Fatal(err)
	}

	id, err := Trash(filepath.Join(mem, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mem, "a.md")); !os.IsNotExist(err) {
		t.Error("the file stays")
	}
	if got := read(t, filepath.Join(mem, "MEMORY.md")); got != "- [B](b.md) — second\n" {
		t.Errorf("index after trash: %q", got)
	}
	entries, _ := tend.MemoryTrash()
	if len(entries) != 1 || entries[0].MemoryID() != id || entries[0].Kind != tend.KindMemory || entries[0].Line != "- [A](a.md) — first" ||
		entries[0].Title != "A" {
		t.Errorf("memory trash: %+v", entries)
	}
	if sessions, _ := tend.LoadTrash(); len(sessions) != 1 || sessions[0].SessionID != "s1" {
		t.Errorf("the session trash lists sessions only, and keeps them: %+v", sessions)
	}

	file, err := Restore(id)
	if err != nil || file != filepath.Join(mem, "a.md") {
		t.Fatalf("Restore = %s, %v", file, err)
	}
	if read(t, file) != "alpha\n" {
		t.Error("content")
	}
	if got := read(t, filepath.Join(mem, "MEMORY.md")); got != "- [B](b.md) — second\n- [A](a.md) — first\n" {
		t.Errorf("index after restore: %q", got)
	}
	if entries, _ := tend.MemoryTrash(); len(entries) != 0 {
		t.Errorf("entry stays: %+v", entries)
	}
	if _, err := Restore(id); err == nil {
		t.Error("restoring twice")
	}
	if sessions, _ := tend.LoadTrash(); len(sessions) != 1 {
		t.Errorf("the session entry survives: %+v", sessions)
	}
}

func TestRestoreRefusesAnExistingFile(t *testing.T) {
	homes(t)
	mem := ClaudeDir(t.TempDir())
	write(t, filepath.Join(mem, "a.md"), "alpha\n")
	id, err := Trash(filepath.Join(mem, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mem, "a.md"), "a new one\n")
	if _, err := Restore(id); err == nil || read(t, filepath.Join(mem, "a.md")) != "a new one\n" {
		t.Errorf("restore over a new file: %v", err)
	}
}

func TestTrashRefusesIndexesAndCodex(t *testing.T) {
	_, codex := homes(t)
	mem := ClaudeDir(t.TempDir())
	write(t, filepath.Join(mem, "MEMORY.md"), "- [A](a.md)\n")
	write(t, filepath.Join(codex, "memories", "MEMORY.md"), "# Task Group: g\n")
	for _, p := range []string{filepath.Join(mem, "MEMORY.md"), filepath.Join(codex, "memories", "MEMORY.md")} {
		if _, err := Trash(p); err == nil {
			t.Errorf("Trash(%s) went through", p)
		}
	}
	outside := filepath.Join(t.TempDir(), "x.md")
	write(t, outside, "x\n")
	if _, err := Trash(outside); !errors.Is(err, ErrOutside) {
		t.Errorf("outside: %v", err)
	}
}

func TestWriteNeverOverwrites(t *testing.T) {
	homes(t)
	mem := ClaudeDir(t.TempDir())
	write(t, filepath.Join(mem, "MEMORY.md"), "- [A](a.md) — first")
	w, err := Write(mem, "b.md", []byte("beta\n"), "- [B](b.md) — second")
	if err != nil || w.Incoming || w.File != filepath.Join(mem, "b.md") || w.Lines != 2 || w.Over {
		t.Fatalf("new: %+v %v", w, err)
	}
	if got := read(t, filepath.Join(mem, "MEMORY.md")); got != "- [A](a.md) — first\n- [B](b.md) — second\n" {
		t.Errorf("index: %q", got)
	}
	if w, err := Write(mem, "b.md", []byte("beta\n"), "- [B](b.md) — second"); err != nil || w.Incoming || w.Lines != 2 {
		t.Errorf("same again: %+v %v", w, err)
	}
	w, err = Write(mem, "b.md", []byte("other beta\n"), "- [B](b.md) — theirs")
	if err != nil || !w.Incoming || w.File != filepath.Join(mem, ".incoming", "b.md") {
		t.Fatalf("different: %+v %v", w, err)
	}
	if read(t, filepath.Join(mem, "b.md")) != "beta\n" || read(t, w.File) != "other beta\n" {
		t.Error("overwrote")
	}
	if got := read(t, filepath.Join(mem, "MEMORY.md")); strings.Contains(got, "theirs") {
		t.Errorf("an incoming one is not indexed: %q", got)
	}
	for _, bad := range []string{"../x.md", "MEMORY.md", ".incoming/x.md", "x.txt", ""} {
		if _, err := Write(mem, bad, []byte("x"), ""); err == nil {
			t.Errorf("Write(%q) went through", bad)
		}
	}
}

// TestLoadListsIncomingApart: what Write put under .incoming/ is listed apart from the memories, and reads.
func TestLoadListsIncomingApart(t *testing.T) {
	homes(t)
	mem := ClaudeDir(t.TempDir())
	write(t, filepath.Join(mem, "MEMORY.md"), "- [B](b.md) — second")
	write(t, filepath.Join(mem, "b.md"), "beta\n")
	w, err := Write(mem, "b.md", []byte("---\nname: theirs\n---\nother beta\n"), "")
	if err != nil || !w.Incoming {
		t.Fatalf("different: %+v %v", w, err)
	}
	s, ok := Load(KindClaude, mem)
	if !ok || len(s.Items) != 1 || s.Items[0].File != filepath.Join(mem, "b.md") {
		t.Fatalf("the memories leave .incoming/ out: %+v", s.Items)
	}
	if len(s.Incoming) != 1 || s.Incoming[0].File != w.File || s.Incoming[0].Title != "theirs" || s.Incoming[0].InIndex || s.Incoming[0].SHA == "" {
		t.Fatalf("the incoming one is listed apart: %+v", s.Incoming)
	}
	if text, _, _, err := Read(w.File); err != nil || !strings.Contains(text, "other beta") {
		t.Errorf("and reads: %q %v", text, err)
	}
	if s, _ := Load(KindCodexGlobal, mem); len(s.Incoming) != 0 {
		t.Errorf("only Claude's sets have one: %+v", s.Incoming)
	}
}

func TestMergeCopiesThenTrashesTheOldDirectory(t *testing.T) {
	homes(t)
	from, to := ClaudeDir(t.TempDir()), ClaudeDir(t.TempDir())
	write(t, filepath.Join(from, "MEMORY.md"), "- [A](a.md) — only here\n- [B](b.md) — both, different\n- [C](c.md) — both, same\n")
	write(t, filepath.Join(from, "a.md"), "alpha\n")
	write(t, filepath.Join(from, "b.md"), "beta here\n")
	write(t, filepath.Join(from, "c.md"), "gamma\n")
	write(t, filepath.Join(to, "MEMORY.md"), "- [B](b.md) — both, different\n- [C](c.md) — both, same\n")
	write(t, filepath.Join(to, "b.md"), "beta there\n")
	write(t, filepath.Join(to, "c.md"), "gamma\n")

	m, err := Merge(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Copied, []string{"a.md"}) || !slices.Equal(m.Incoming, []string{"b.md"}) || !slices.Equal(m.Same, []string{"c.md"}) || m.Trashed == "" {
		t.Errorf("merge: %+v", m)
	}
	if read(t, filepath.Join(to, "a.md")) != "alpha\n" || read(t, filepath.Join(to, "b.md")) != "beta there\n" ||
		read(t, filepath.Join(to, ".incoming", "b.md")) != "beta here\n" {
		t.Error("files")
	}
	if got := read(t, filepath.Join(to, "MEMORY.md")); got != "- [B](b.md) — both, different\n- [C](c.md) — both, same\n- [A](a.md) — only here\n" {
		t.Errorf("index: %q", got)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Error("the old directory stays")
	}
	if entries, _ := tend.MemoryTrash(); len(entries) != 1 || entries[0].MemoryID() != m.Trashed {
		t.Errorf("trash: %+v", entries)
	}
	if _, err := Restore(m.Trashed); err != nil || read(t, filepath.Join(from, "a.md")) != "alpha\n" {
		t.Errorf("restoring the directory: %v", err)
	}
}

func TestMergeKeepsADirectoryWithOtherFiles(t *testing.T) {
	homes(t)
	from, to := ClaudeDir(t.TempDir()), ClaudeDir(t.TempDir())
	write(t, filepath.Join(from, "a.md"), "alpha\n")
	write(t, filepath.Join(from, "data.json"), "{}\n")
	m, err := Merge(from, to)
	if err != nil || m.Trashed != "" || !slices.Equal(m.Copied, []string{"a.md"}) || !slices.Equal(m.Left, []string{"data.json"}) {
		t.Errorf("merge: %+v %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(from, "a.md")); err != nil {
		t.Error("nothing leaves a directory that is kept")
	}
}

func TestScanSortsOrphans(t *testing.T) {
	claude, _ := homes(t)
	work := t.TempDir()
	scratch := filepath.Join(work, "claude-501", "scratchpad")
	temporary = func(dir string) bool { return strings.Contains(dir, "scratchpad") }
	t.Cleanup(func() { temporary = isTemporary })
	alive := filepath.Join(work, "alive")
	os.MkdirAll(alive, 0o755)
	movedTo := filepath.Join(work, "moved-here")
	os.MkdirAll(movedTo, 0o755)
	gone, lost, twice := filepath.Join(work, "gone"), filepath.Join(work, "lost"), filepath.Join(work, "twice")
	dup1, dup2 := filepath.Join(work, "dup1"), filepath.Join(work, "dup2")
	os.MkdirAll(dup1, 0o755)
	os.MkdirAll(dup2, 0o755)
	mem := func(dir string) string {
		return filepath.Join(claude, "projects", index.ClaudeProjectName(dir), "memory")
	}
	for _, d := range []string{alive, scratch, gone, lost, twice} {
		write(t, filepath.Join(mem(d), "a.md"), "a\n")
	}
	write(t, filepath.Join(mem(alive), "MEMORY.md"), strings.Repeat("- [a](a.md)\n", 201))
	os.MkdirAll(mem(filepath.Join(work, "empty")), 0o755)
	unseen := filepath.Join(work, "no session", ".hidden-dir")
	os.MkdirAll(unseen, 0o755)
	write(t, filepath.Join(mem(unseen), "a.md"), "a\n")
	const remote, other = "https://example.com/acme/app.git", "git@example.com:acme/other.git"
	origins := []Origin{{Dir: alive}, {Dir: scratch}, {Dir: gone, Remote: remote}, {Dir: movedTo, Remote: remote},
		{Dir: lost}, {Dir: twice, Remote: other}, {Dir: dup1, Remote: other}, {Dir: dup2, Remote: other}}
	checkouts := map[string][]string{remote: {movedTo}, other: {dup1, dup2}}
	r := Scan(origins, func(remote string) []string { return checkouts[remote] })

	if r.Dirs != 6 || r.Empty != 1 {
		t.Errorf("dirs %d empty %d", r.Dirs, r.Empty)
	}
	if !slices.Equal(r.Over, []string{filepath.Join(mem(alive), "MEMORY.md")}) {
		t.Errorf("over: %v", r.Over)
	}
	got := map[string]Orphan{}
	for _, o := range r.Orphans {
		got[o.Dir] = o
	}
	if len(got) != 4 {
		t.Fatalf("orphans: %+v", r.Orphans)
	}
	if o := got[mem(scratch)]; o.Class != OrphanTemp || o.From != scratch {
		t.Errorf("scratch: %+v", o)
	}
	if o := got[mem(gone)]; o.Class != OrphanMoved || o.Target != movedTo || o.From != gone {
		t.Errorf("moved: %+v", o)
	}
	if o := got[mem(lost)]; o.Class != OrphanUnknown {
		t.Errorf("no remote: %+v", o)
	}
	if o := got[mem(twice)]; o.Class != OrphanUnknown {
		t.Errorf("two checkouts of the remote: %+v", o)
	}
	for _, d := range []string{alive, unseen} {
		if _, ok := got[mem(d)]; ok {
			t.Errorf("%s is still there: no orphan", d)
		}
	}
}

func TestScanWithoutAnyOrigin(t *testing.T) {
	claude, _ := homes(t)
	write(t, filepath.Join(claude, "projects", "-nowhere-known", "memory", "a.md"), "a\n")
	r := Scan(nil, nil)
	if len(r.Orphans) != 1 || r.Orphans[0].Class != OrphanUnknown || r.Orphans[0].From != "" {
		t.Errorf("orphans: %+v", r.Orphans)
	}
}
