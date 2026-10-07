package memory

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oxsean/fav/internal/pathmap"
)

func item(file, text string) Item {
	return Item{File: file, Title: filepath.Base(file), SHA: digest([]byte(text)), Norm: digest(normalize([]byte(text)))}
}

func names(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

// Two machines' memories of one project, paired by kind and file name: what only one side has, what differs, and
// what is the same, exactly or once line endings and the explicitly mapped directories and homes are set aside.
func TestDiffPairsByKindAndName(t *testing.T) {
	here := pathmap.End{OS: "darwin", Home: "/Users/me"}
	there := pathmap.End{OS: "windows", Home: `C:\Users\me`}
	m := Mapping{Here: here, There: there, Pairs: [][2]string{{"/Users/me/dev/webapp", `D:\src\webapp`}}}
	texts := map[string]string{
		"/h/same.md": "same\n", `C:\t\same.md`: "same\n",
		"/h/crlf.md": "one\ntwo\n", `C:\t\crlf.md`: "one\r\ntwo\r\n",
		"/h/paths.md":    "Build in /Users/me/dev/webapp/src/cmd, config in /Users/me/.config/x.\n",
		`C:\t\paths.md`:  "Build in D:\\src\\webapp\\src\\cmd, config in C:\\Users\\me\\.config\\x.\r\n",
		"/h/unmapped.md": "Lives in /opt/webapp.\n", `C:\t\unmapped.md`: "Lives in E:\\webapp.\n",
		"/h/differ.md": "ours\n", `C:\t\differ.md`: "theirs\n",
		"/h/mine.md": "only here\n", `C:\t\theirs.md`: "only there\n",
		"/h/MEMORY.md":   "# Task Group: callback\napplies_to: cwd=/Users/me/dev/webapp/src\n\n# Task Group: other\n",
		`C:\t\MEMORY.md`: "# Task Group: callback\r\napplies_to: cwd=D:\\src\\webapp\\src\r\n",
	}
	var hs, ts Set
	hs.Kind, ts.Kind = KindClaude, KindClaude
	for _, n := range []string{"same", "crlf", "paths", "unmapped", "differ", "mine"} {
		hs.Items = append(hs.Items, item("/h/"+n+".md", texts["/h/"+n+".md"]))
	}
	for _, n := range []string{"same", "crlf", "paths", "unmapped", "differ", "theirs"} {
		ts.Items = append(ts.Items, item(`C:\t\`+n+".md", texts[`C:\t\`+n+".md"]))
	}
	hc := Set{Kind: KindCodexGlobal, Items: []Item{{File: "/h/MEMORY.md", Title: "callback", Line: 1, SHA: "a", Norm: "a"},
		{File: "/h/MEMORY.md", Title: "other", Line: 4, SHA: "b", Norm: "b"}}}
	tc := Set{Kind: KindCodexGlobal, Items: []Item{{File: `C:\t\MEMORY.md`, Title: "callback", Line: 1, SHA: "c", Norm: "d"}}}
	var read []string
	text := func(onThere bool, it Item) (string, error) {
		read = append(read, it.File)
		b, ok := texts[it.File]
		if !ok {
			return "", errors.New("no such file")
		}
		if it.Line > 0 {
			return Block(b, it.Line), nil
		}
		return b, nil
	}
	d := Diff([]Set{hs, hc}, []Set{ts, tc}, m, text)

	if got := names(d.OnlyHere); !slices.Equal(got, []string{"mine.md", "other"}) {
		t.Errorf("only here: %v", got)
	}
	if got := names(d.OnlyThere); !slices.Equal(got, []string{"theirs.md"}) {
		t.Errorf("only there: %v", got)
	}
	if got := names(d.Differ); !slices.Equal(got, []string{"differ.md", "unmapped.md"}) {
		t.Errorf("differ: %v", got)
	}
	if got := names(d.Same); !slices.Equal(got, []string{"crlf.md", "paths.md", "same.md", "callback"}) {
		t.Fatalf("same: %v", got)
	}
	for _, e := range d.Same {
		if loose := e.Name != "same.md"; e.Loose != loose {
			t.Errorf("%s: loose %v", e.Name, e.Loose)
		}
		if e.Here == nil || e.There == nil {
			t.Errorf("%s: both sides kept: %+v", e.Name, e)
		}
	}
	if e := d.Differ[0]; e.Kind != KindClaude || e.Here.File != "/h/differ.md" || e.There.File != `C:\t\differ.md` {
		t.Errorf("an entry keeps both items: %+v", e)
	}
	for _, f := range read {
		if f == "/h/same.md" || f == "/h/crlf.md" || f == "/h/mine.md" {
			t.Errorf("read %s: texts are read only where the hashes disagree", f)
		}
	}
}

// Without a mapping nothing is rewritten: only line endings are set aside, and a text that cannot be read differs.
func TestDiffWithoutMappingOrText(t *testing.T) {
	a := Set{Kind: KindClaude, Items: []Item{item("/a/x.md", "in /a\n"), item("/a/y.md", "y\n")}}
	b := Set{Kind: KindClaude, Items: []Item{item("/b/x.md", "in /b\n"), item("/b/y.md", "y\r\n")}}
	d := Diff([]Set{a}, []Set{b}, Mapping{}, func(bool, Item) (string, error) { return "", errors.New("unreadable") })
	if !slices.Equal(names(d.Differ), []string{"x.md"}) || !slices.Equal(names(d.Same), []string{"y.md"}) || !d.Same[0].Loose {
		t.Errorf("diff: %+v", d)
	}
	m := Mapping{Here: pathmap.End{OS: "linux", Home: "/a"}, There: pathmap.End{OS: "linux", Home: "/b"}}
	if d := Diff([]Set{a}, []Set{b}, m, func(bool, Item) (string, error) { return "", errors.New("unreadable") }); len(d.Differ) != 1 {
		t.Errorf("an unreadable text is not taken as the same: %+v", d)
	}
}

func TestMapPathsOnlyAtBoundaries(t *testing.T) {
	m := Mapping{Here: pathmap.End{OS: "linux", Home: "/home/me"}, There: pathmap.End{OS: "darwin", Home: "/Users/me"},
		Pairs: [][2]string{{"/home/me/dev/app", "/Users/me/src/app"}}}
	for in, want := range map[string]string{
		"see /Users/me/src/app/x.go:12.":   "see /home/me/dev/app/x.go:12.",
		"`/Users/me/src/app`":              "`/home/me/dev/app`",
		"/Users/me/notes and /Users/meow/": "/home/me/notes and /Users/meow/",
		"/opt/Users/me/x":                  "/opt/Users/me/x",
		"/Users/me/src/apple":              "/home/me/src/apple",
	} {
		if got := mapPaths(in, m); got != want {
			t.Errorf("mapPaths(%q) = %q, want %q", in, got, want)
		}
	}
}

// A block is its lines up to the next group, with LF endings and without the blank lines that part it from the next.
func TestBlockIsOneTaskGroup(t *testing.T) {
	text := "# Task Group: a\r\nscope: x\r\n\r\n# Task Group: b\r\nscope: y\r\n"
	if got := Block(text, 1); got != "# Task Group: a\nscope: x\n" {
		t.Errorf("first: %q", got)
	}
	if got := Block(text, 4); got != "# Task Group: b\nscope: y\n" {
		t.Errorf("last: %q", got)
	}
	if got := Block(text, 9); got != "" {
		t.Errorf("past the end: %q", got)
	}
}

// Codex's global blocks carry the hash of their own text, so two machines compare them as they do Claude's files.
func TestCodexBlocksAreHashed(t *testing.T) {
	_, codex := homes(t)
	dir := t.TempDir()
	write(t, filepath.Join(codex, "memories", "MEMORY.md"), globalFixture(dir, t.TempDir()))
	sets := List([]string{dir}, true)
	i := slices.IndexFunc(sets, func(s Set) bool { return s.Kind == KindCodexGlobal && s.Dir == dir })
	if i < 0 || len(sets[i].Items) != 1 {
		t.Fatalf("sets: %+v", sets)
	}
	b := read(t, filepath.Join(codex, "memories", "MEMORY.md"))
	if it := sets[i].Items[0]; it.Norm != digest([]byte(Block(b, it.Line))) || it.SHA == "" {
		t.Errorf("block hash: %+v", it)
	}
}

func TestPutGuardsWhatTheCallerSaw(t *testing.T) {
	homes(t)
	proj := t.TempDir()
	mem := ClaudeDir(proj)
	write(t, filepath.Join(mem, "MEMORY.md"), "- [A](a.md) — first\n")
	write(t, filepath.Join(mem, "a.md"), "alpha\n")

	w, err := Put(proj, "b.md", []byte("beta\n"), "- [B](b.md) — second", "")
	if err != nil || w.Incoming || w.File != filepath.Join(mem, "b.md") || w.Lines != 2 {
		t.Fatalf("new: %+v %v", w, err)
	}
	if _, err := Put(proj, "b.md", []byte("beta 2\n"), "- [B](b.md) — second", ""); !errors.Is(err, ErrStale) {
		t.Errorf("expect none, one there: %v", err)
	}
	if _, err := Put(proj, "a.md", []byte("alpha 2\n"), "", digest([]byte("something else\n"))); !errors.Is(err, ErrStale) {
		t.Errorf("expect another one: %v", err)
	}
	if _, err := Put(proj, "c.md", []byte("c\n"), "", digest([]byte("c\n"))); !errors.Is(err, ErrStale) {
		t.Errorf("expect one, none there: %v", err)
	}
	w, err = Put(mem, "a.md", []byte("alpha theirs\n"), "- [A](a.md) — theirs", digest([]byte("alpha\n")))
	if err != nil || !w.Incoming || w.File != filepath.Join(mem, ".incoming", "a.md") {
		t.Fatalf("different: %+v %v", w, err)
	}
	if read(t, filepath.Join(mem, "a.md")) != "alpha\n" || read(t, filepath.Join(mem, "MEMORY.md")) != "- [A](a.md) — first\n- [B](b.md) — second\n" {
		t.Error("the one there and its index line stay")
	}
	for _, bad := range []struct{ dir, name, line string }{
		{"relative", "x.md", ""},
		{filepath.Join(proj, "missing"), "x.md", ""},
		{proj, "../x.md", ""},
		{proj, "x.md", "- [X](x.md)\n- [Y](y.md)"},
		{proj, "x.md", "- [Y](y.md) — another file"},
		{proj, "x.md", "free text"},
	} {
		if _, err := Put(bad.dir, bad.name, []byte("x\n"), bad.line, ""); err == nil || errors.Is(err, ErrStale) {
			t.Errorf("Put(%q, %q, line %q): %v", bad.dir, bad.name, bad.line, err)
		}
	}
}

// .incoming/ holds one copy per different text: a second incoming text of the same name goes beside the first.
func TestWriteKeepsEachIncomingText(t *testing.T) {
	homes(t)
	mem := ClaudeDir(t.TempDir())
	write(t, filepath.Join(mem, "a.md"), "ours\n")
	first, err := Write(mem, "a.md", []byte("from one\n"), "")
	if err != nil || first.File != filepath.Join(mem, ".incoming", "a.md") {
		t.Fatalf("first: %+v %v", first, err)
	}
	if again, err := Write(mem, "a.md", []byte("from one\n"), ""); err != nil || again.File != first.File {
		t.Errorf("the same text again: %+v %v", again, err)
	}
	second, err := Write(mem, "a.md", []byte("from two\n"), "")
	if err != nil || second.File != filepath.Join(mem, ".incoming", "a-2.md") || !second.Incoming {
		t.Fatalf("second: %+v %v", second, err)
	}
	if read(t, first.File) != "from one\n" || read(t, second.File) != "from two\n" {
		t.Error("an incoming text was overwritten")
	}
}

// An index that already points at the file keeps its own line: one line per memory.
func TestWriteAddsNoSecondLineForAFile(t *testing.T) {
	homes(t)
	mem := ClaudeDir(t.TempDir())
	write(t, filepath.Join(mem, "MEMORY.md"), "- [A](a.md) — ours\n")
	if _, err := Write(mem, "a.md", []byte("alpha\n"), "- [A](a.md) — theirs"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(mem, "MEMORY.md")); got != "- [A](a.md) — ours\n" {
		t.Errorf("index: %q", got)
	}
	if _, err := os.Stat(filepath.Join(mem, "a.md")); err != nil {
		t.Error(err)
	}
}
