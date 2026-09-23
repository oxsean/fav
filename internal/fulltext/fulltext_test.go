package fulltext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func claudeLine(role, text string) string {
	return `{"type":"` + role + `","timestamp":"2026-09-22T10:00:00Z","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
}

func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(strings.Join(lines, ""))
	f.Close()
}

func textLines(t *testing.T, dir, path string) []string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, textName(path)))
	var out []string
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			_, _, _, text, _ := fields([]byte(l))
			out = append(out, string(text))
		}
	}
	return out
}

func TestUpdateIsIncrementalAndFollowsTheTranscripts(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	a, b := filepath.Join(src, "a.jsonl"), filepath.Join(src, "b.jsonl")
	writeTranscript(t, a, claudeLine("user", "first question"), claudeLine("assistant", "first answer"))
	writeTranscript(t, b, claudeLine("user", "other session"))
	ctx := context.Background()

	p, err := Update(ctx, dir, []string{a, b}, Options{}, nil)
	if err != nil || p.Total != 2 || p.Done != 2 {
		t.Fatalf("first build: %+v %v", p, err)
	}
	if got := textLines(t, dir, a); strings.Join(got, "|") != "first question|first answer" {
		t.Fatalf("a: %q", got)
	}

	if p, _ := Update(ctx, dir, []string{a, b}, Options{}, nil); p.Total != 0 {
		t.Fatalf("nothing changed, nothing read: %+v", p)
	}

	appendTranscript(t, a, claudeLine("user", "second question"))
	if p, _ := Update(ctx, dir, []string{a, b}, Options{}, nil); p.Total != 1 {
		t.Fatalf("only the grown transcript is read: %+v", p)
	}
	if got := textLines(t, dir, a); len(got) != 3 || got[2] != "second question" {
		t.Fatalf("appended once, no duplicates: %q", got)
	}

	writeTranscript(t, a, claudeLine("user", "rewritten"))
	Update(ctx, dir, []string{a, b}, Options{}, nil)
	if got := textLines(t, dir, a); strings.Join(got, "|") != "rewritten" {
		t.Fatalf("a shorter transcript is rebuilt: %q", got)
	}

	writeTranscript(t, a, claudeLine("user", "rewritten in place, now longer"), claudeLine("assistant", "tail"))
	Update(ctx, dir, []string{a, b}, Options{}, nil)
	if got := textLines(t, dir, a); strings.Join(got, "|") != "rewritten in place, now longer|tail" {
		t.Fatalf("a transcript rewritten longer is rebuilt, not resumed mid-line: %q", got)
	}

	writeTranscript(t, a, claudeLine("user", "rewritten in place, same sizE!"), claudeLine("assistant", "tail"))
	os.Chtimes(a, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	Update(ctx, dir, []string{a, b}, Options{}, nil)
	if got := textLines(t, dir, a); strings.Join(got, "|") != "rewritten in place, same sizE!|tail" {
		t.Fatalf("a same-size rewrite is noticed by its mtime: %q", got)
	}

	os.Remove(filepath.Join(dir, textName(a)))
	Update(ctx, dir, []string{a, b}, Options{}, nil)
	if got := textLines(t, dir, a); len(got) != 2 {
		t.Fatalf("a lost text file is rebuilt: %q", got)
	}

	Update(ctx, dir, []string{a}, Options{}, nil)
	if _, err := os.Stat(filepath.Join(dir, textName(b))); !os.IsNotExist(err) {
		t.Fatal("a transcript that left the index loses its text")
	}
}

func TestUpdateRecoversFromARunThatDiedBeforeSavingState(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	a := filepath.Join(src, "a.jsonl")
	writeTranscript(t, a, claudeLine("user", "kept"))
	ctx := context.Background()
	Update(ctx, dir, []string{a}, Options{}, nil)

	// the dead run appended its text but its state never reached disk
	f, _ := os.OpenFile(filepath.Join(dir, textName(a)), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("99\tu\t0\tghost\n")
	f.Close()
	appendTranscript(t, a, claudeLine("user", "new"))
	Update(ctx, dir, []string{a}, Options{}, nil)
	if got := textLines(t, dir, a); strings.Join(got, "|") != "kept|new" {
		t.Fatalf("the unsaved tail is cut before appending: %q", got)
	}
}

func TestUpdateStopsWhenTheBudgetEnds(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	var paths []string
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(src, n+".jsonl")
		writeTranscript(t, p, claudeLine("user", n))
		paths = append(paths, p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	Update(ctx, dir, paths, Options{}, func(Progress) {
		calls++
		cancel()
	})
	if calls != 1 {
		t.Fatalf("a cancelled update stops after the transcript in hand, got %d", calls)
	}
	if p, _ := Update(context.Background(), dir, paths, Options{}, nil); p.Total != 2 {
		t.Fatalf("the next update picks up the rest: %+v", p)
	}
}

func TestKeywordsBigramsAndTheSixtyPercentRule(t *testing.T) {
	kw := Keywords("分页游标漂移 Flyway")
	if len(kw) != 2 || strings.Join(kw[0].Terms, ",") != "分页,页游,游标,标漂,漂移" || kw[1].Terms[0] != "flyway" {
		t.Fatalf("keywords: %+v", kw)
	}
	for text, want := range map[string]bool{
		"排查分页游标漂移":          true,  // verbatim
		"游标分页之后出现漂移":        true,  // other order: 分页 游标 漂移 = 3 of 5
		"游标漂移":              true,  // 游标 标漂 漂移 = 3 of 5
		"只提到分页":             false, // 1 of 5
		"page cursor drift": false,
	} {
		if got := kw[0].Match(lowerASCII(text)); got != want {
			t.Errorf("%q: got %v want %v", text, got, want)
		}
	}
	if !(Query{Kws: kw}).MatchEntry('u', "FLYWAY baseline") {
		t.Error("Latin words match case-insensitively")
	}
}

func TestSpansMergeOverlappingBigrams(t *testing.T) {
	text := "先看分页游标漂移，再看 Flyway 日志"
	sp := Spans(Keywords("分页游标漂移 flyway"), text)
	var got []string
	for _, s := range sp {
		got = append(got, text[s[0]:s[1]])
	}
	if strings.Join(got, "|") != "分页游标漂移|Flyway" {
		t.Fatalf("spans: %q", got)
	}
}

func TestSearchNeedsEveryKeywordInTheSession(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	one := filepath.Join(src, "one.jsonl") // both keywords in one message
	spread := filepath.Join(src, "spread.jsonl")
	half := filepath.Join(src, "half.jsonl")
	writeTranscript(t, one, claudeLine("user", "滚轮加速怎么调"), claudeLine("assistant", "看设置面板"))
	writeTranscript(t, spread, claudeLine("user", "滚轮太慢"), claudeLine("assistant", "加上加速就好"))
	writeTranscript(t, half, claudeLine("user", "只有滚轮"))
	Update(context.Background(), dir, []string{one, spread, half}, Options{}, nil)

	cands := []Cand{{Paths: []string{half}}, {Paths: []string{spread}}, {Paths: []string{one}}}
	res := Search(context.Background(), dir, cands, "滚轮 加速")
	if len(res) != 2 {
		t.Fatalf("sessions holding both keywords: %+v", res)
	}
	if res[0].Cand != 2 || !res[0].AllInOne || res[1].Cand != 1 || res[1].AllInOne {
		t.Fatalf("the session with one message holding both ranks first: %+v", res)
	}
	if !strings.Contains(res[0].Snippet, "滚轮加速") || res[1].Hits != 2 {
		t.Fatalf("snippet and hit count: %+v", res)
	}
}

func TestSplitKeepsFiltersOutOfTheKeywords(t *testing.T) {
	kw, scope := Split("游标 project:notes-api #pagination last:7d drift")
	if kw != "游标 drift" || scope != "project:notes-api #pagination last:7d status:all turns:0" {
		t.Fatalf("kw=%q scope=%q", kw, scope)
	}
	if _, scope := Split("x status:done turns:5"); scope != "status:done turns:5" {
		t.Fatalf("explicit status and turns stay: %q", scope)
	}
	if q, ok := Prefixed("》 游标"); !ok || q != "游标" {
		t.Fatalf("the CJK chevron also switches to message search: %q %v", q, ok)
	}
}

func TestResultPointsAtTheBestHitAndHitsListsThemAll(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	a := filepath.Join(src, "a.jsonl")
	first, second := claudeLine("user", "滚轮太慢"), claudeLine("assistant", "滚轮加速做好了")
	writeTranscript(t, a, first, second, claudeLine("user", "无关"))
	ctx := context.Background()
	Update(ctx, dir, []string{a}, Options{}, nil)
	res := Search(ctx, dir, []Cand{{Paths: []string{a}}}, "滚轮 加速")
	if len(res) != 1 || res[0].Path != a || res[0].Off != int64(len(first)) {
		t.Fatalf("the result points at the message holding both keywords: %+v", res)
	}
	hs, total := Hits(ctx, dir, []string{a}, "滚轮 加速", 0, Hit{})
	if total != 2 || len(hs) != 2 || hs[0].Text != "滚轮加速做好了" || hs[1].Off != 0 || hs[0].Role != 'a' {
		t.Fatalf("every matching message, newest first: %+v", hs)
	}
	if hs, total := Hits(ctx, dir, []string{a}, "滚轮 加速", 1, Hit{Path: a, Off: 0}); total != 2 || len(hs) != 1 || hs[0].Off != 0 {
		t.Fatalf("the pinned hit stays when the limit cuts: %+v", hs)
	}
}

func TestRewritesBehindAPartialLineOrAnUnchangedHeadAreRebuilt(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	a := filepath.Join(src, "a.jsonl")
	ctx := context.Background()
	head := claudeLine("user", strings.Repeat("h", 5000)) // longer than the hashed head
	writeTranscript(t, a, head, claudeLine("user", "old middle"), `{"type":"user","partial`)
	Update(ctx, dir, []string{a}, Options{}, nil)
	writeTranscript(t, a, head, claudeLine("user", "new middle"), `{"type":"user","partial`)
	os.Chtimes(a, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	Update(ctx, dir, []string{a}, Options{}, nil)
	if got := textLines(t, dir, a); len(got) != 2 || got[1] != "new middle" {
		t.Fatalf("a same-size rewrite behind a partial last line is rebuilt: %q", got)
	}
	writeTranscript(t, a, head, claudeLine("user", "newer, longer middle"), claudeLine("assistant", "more"))
	Update(ctx, dir, []string{a}, Options{}, nil)
	if got := textLines(t, dir, a); strings.Join(got[1:], "|") != "newer, longer middle|more" {
		t.Fatalf("a longer rewrite with the same head is rebuilt: %q", got[1:])
	}
}

func TestKeywordsCloseTogetherRankFirst(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	far, near := filepath.Join(src, "far.jsonl"), filepath.Join(src, "near.jsonl")
	filler := strings.Repeat("无关的内容", 60)
	writeTranscript(t, far, claudeLine("user", "滚轮"+filler+"加速"))
	writeTranscript(t, near, claudeLine("user", filler+"滚轮加速"))
	Update(context.Background(), dir, []string{far, near}, Options{}, nil)
	res := Search(context.Background(), dir, []Cand{{Paths: []string{far}}, {Paths: []string{near}}}, "滚轮 加速")
	if len(res) != 2 || res[0].Cand != 1 {
		t.Fatalf("the message with the keywords side by side ranks first: %+v", res)
	}
	if w := window([]byte("滚轮xx加速"), 3, [][]int{{0}, {1}}, [][]byte{[]byte("滚轮"), []byte("加速")}, 3); w != len("滚轮xx加速") {
		t.Fatalf("window spans both keywords: %d", w)
	}
}

func linedAt(role, text string, at time.Time, extra string) string {
	return `{"type":"` + role + `","timestamp":"` + at.UTC().Format(time.RFC3339) + `"` + extra + `,"message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
}

func TestRankingWeighsWhenWhoAndWhatTheSessionIsAbout(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	now := time.Now()
	path := func(n string) string { return filepath.Join(src, n+".jsonl") }
	old, fresh, recap, mine, titled := path("old"), path("fresh"), path("recap"), path("mine"), path("titled")
	writeTranscript(t, old, linedAt("user", "游标漂移", now.AddDate(-1, 0, 0), ""))
	writeTranscript(t, fresh, linedAt("user", "游标漂移", now, ""))
	writeTranscript(t, recap, linedAt("user", "游标漂移", now, `,"isCompactSummary":true`))
	writeTranscript(t, mine, linedAt("user", "游标漂移", now.AddDate(0, 0, -1), ""), linedAt("assistant", "游标漂移", now, ""))
	writeTranscript(t, titled, linedAt("assistant", "游标漂移", now.AddDate(0, 0, -1), ""))
	ctx := context.Background()
	Update(ctx, dir, []string{old, fresh, recap, mine, titled}, Options{}, nil)
	search := func(cands ...Cand) []Result { return Search(ctx, dir, cands, "游标漂移") }

	if res := search(Cand{Paths: []string{old}}, Cand{Paths: []string{fresh}}); res[0].Cand != 1 {
		t.Fatalf("the same hit, newer first: %+v", res)
	}
	if res := search(Cand{Paths: []string{recap}}, Cand{Paths: []string{old}}); res[0].Cand != 1 {
		t.Fatalf("a context recap ranks below a real message, even an old one: %+v", res)
	}
	if res := search(Cand{Paths: []string{mine}}); res[0].At.After(now.Add(-time.Hour)) {
		t.Fatalf("the card shows what the user said, not the newer assistant echo: %+v", res)
	}
	res := search(Cand{Paths: []string{titled}}, Cand{Paths: []string{titled}, Meta: "排查游标漂移"})
	if res[0].Cand != 1 {
		t.Fatalf("a session titled after the keywords ranks first: %+v", res)
	}
}

func TestQuotedKeywordsMatchVerbatim(t *testing.T) {
	kw := Keywords(`"标题恢复" “Cursor Drift” 「分页」 "没关上`)
	if len(kw) != 4 || !kw[0].Exact || kw[1].Raw != "cursor drift" || kw[2].Raw != "分页" || kw[3].Raw != "没关上" {
		t.Fatalf("quoted keywords: %+v", kw)
	}
	if kw[0].Match(lowerASCII("恢复了标题")) || !kw[0].Match(lowerASCII("先把标题恢复一下")) {
		t.Error("a quoted keyword needs its characters together and in order")
	}
	if !Keywords("标题恢复")[0].Match(lowerASCII("恢复了标题")) {
		t.Error("unquoted, word order does not matter")
	}
	sp := Spans(Keywords(`"标题恢复"`), "标题…恢复，标题恢复")
	if len(sp) != 1 {
		t.Errorf("only the verbatim run is highlighted: %v", sp)
	}
	kws, scope := Split(`"cursor drift" project:x 漂移`)
	if kws != `"cursor drift" 漂移` || scope != "project:x status:all turns:0" {
		t.Fatalf("a quoted phrase stays one keyword: %q %q", kws, scope)
	}

	dir, src := t.TempDir(), t.TempDir()
	a, b := filepath.Join(src, "a.jsonl"), filepath.Join(src, "b.jsonl")
	writeTranscript(t, a, claudeLine("user", "恢复了标题"))
	writeTranscript(t, b, claudeLine("user", "先把标题恢复一下"))
	Update(context.Background(), dir, []string{a, b}, Options{}, nil)
	res := Search(context.Background(), dir, []Cand{{Paths: []string{a}}, {Paths: []string{b}}}, `"标题恢复"`)
	if len(res) != 1 || res[0].Cand != 1 {
		t.Fatalf("only the verbatim session: %+v", res)
	}
}

func TestQuerySyntaxExcludesAlternativesAndSpeakers(t *testing.T) {
	q := ParseQuery(`flyway -baseline 迁移|migration who:me "a b"`)
	if len(q.Kws) != 3 || len(q.Neg) != 1 || q.Roles != "u" || len(q.Kws[1].Alts) != 2 || !q.Kws[2].Exact {
		t.Fatalf("parsed: %+v", q)
	}
	if !q.Kws[1].Match(lowerASCII("Flyway Migration done")) || !q.Kws[1].Match("数据迁移") {
		t.Error("a|b matches either")
	}
	if q.MatchEntry('u', "flyway baseline 修好了") || !q.MatchEntry('u', "flyway 修好了") || q.MatchEntry('a', "flyway 修好了") {
		t.Error("-x excludes the message, who:me keeps only the user's")
	}

	dir, src := t.TempDir(), t.TempDir()
	a, b := filepath.Join(src, "a.jsonl"), filepath.Join(src, "b.jsonl")
	writeTranscript(t, a, claudeLine("user", "flyway baseline 坏了"), claudeLine("assistant", "flyway 修好了"))
	writeTranscript(t, b, claudeLine("user", "flyway 升级"))
	ctx := context.Background()
	Update(ctx, dir, []string{a, b}, Options{}, nil)
	cands := []Cand{{Paths: []string{a}}, {Paths: []string{b}}}
	if res := Search(ctx, dir, cands, "flyway -baseline who:me"); len(res) != 1 || res[0].Cand != 1 {
		t.Fatalf("only b has a message of the user's with flyway and without baseline: %+v", res)
	}
	if res := Search(ctx, dir, cands, "升级|坏了"); len(res) != 2 {
		t.Fatalf("either alternative: %+v", res)
	}
	if hs, _ := Hits(ctx, dir, []string{a}, "flyway -baseline", 0, Hit{}); len(hs) != 1 || hs[0].Role != 'a' {
		t.Fatalf("the hit list drops excluded messages: %+v", hs)
	}
}

func TestOneEdit(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"flyawy", "flyway", true}, {"herdrr", "herdr", true}, {"flywy", "flyway", true}, {"flyxay", "flyway", true},
		{"flyway", "flyway", false}, {"fylawy", "flyway", false}, {"abcdef", "abcfed", false}} {
		if got := oneEdit(c.a, c.b); got != c.want {
			t.Errorf("oneEdit(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestATypoFindsTheWordTheSessionsUse(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	a, b := filepath.Join(src, "a.jsonl"), filepath.Join(src, "b.jsonl")
	var lines []string
	for range 6 {
		lines = append(lines, claudeLine("user", "flyway baseline"))
	}
	writeTranscript(t, a, lines...)
	writeTranscript(t, b, claudeLine("user", "claude clause"))
	ctx := context.Background()
	Update(ctx, dir, []string{a, b}, Options{}, nil)

	q := Expand(dir, ParseQuery("flyawy"))
	if fixes := q.Fixes(); len(fixes) != 1 || fixes[0] != "flyway" {
		t.Fatalf("flyawy is read as flyway: %v", fixes)
	}
	res := Search(ctx, dir, []Cand{{Paths: []string{a}}, {Paths: []string{b}}}, "flyawy")
	if len(res) != 1 || res[0].Cand != 0 || !strings.Contains(res[0].Snippet, "flyway") {
		t.Fatalf("the typo finds the session: %+v", res)
	}
	if fixes := Expand(dir, ParseQuery(`"flyawy" clause`)).Fixes(); len(fixes) != 0 {
		t.Fatalf("quoted or known words are left alone: %v", fixes)
	}
	if sp := Spans(q.Kws, "flyway baseline"); len(sp) != 1 {
		t.Fatalf("the corrected word is highlighted: %v", sp)
	}
}

func TestToolOutputIsSearchableWhenAsked(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	a := filepath.Join(src, "a.jsonl")
	writeTranscript(t, a, claudeLine("assistant", "running the tests"),
		`{"type":"user","timestamp":"2026-09-22T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","content":"line one\nNullPointerException at Foo.java:12\nline three\nline four"}]}}`+"\n")
	ctx := context.Background()
	cands := []Cand{{Paths: []string{a}}}
	Update(ctx, dir, []string{a}, Options{}, nil)
	if res := Search(ctx, dir, cands, "NullPointerException"); len(res) != 0 {
		t.Fatalf("outputs are off by default: %+v", res)
	}
	Update(ctx, dir, []string{a}, Options{OutLines: 2}, nil)
	res := Search(ctx, dir, cands, "NullPointerException")
	if len(res) != 1 {
		t.Fatal("with two lines kept, the error in the second line is found")
	}
	if hs, _ := Hits(ctx, dir, []string{a}, "NullPointerException who:tool", 0, Hit{}); len(hs) != 1 || hs[0].Role != 'o' {
		t.Fatalf("an output hit, found by who:tool: %+v", hs)
	}
	Update(ctx, dir, []string{a}, Options{OutLines: 1}, nil)
	if res := Search(ctx, dir, cands, "NullPointerException"); len(res) != 0 {
		t.Fatal("changing the line count rebuilds: the second line is gone")
	}
}
