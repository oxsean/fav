package render

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
)

type ownFiles struct{ r *fav.Rec }

func (o ownFiles) Pulse() (capture.Pulse, bool) { return capture.ReadPulse(o.r.TranscriptPath) }
func (o ownFiles) Checks() []capture.Check      { return capture.Checks(o.r) }

type farSource struct{ checks []capture.Check }

func (f farSource) Pulse() (capture.Pulse, bool) { return capture.Pulse{Prompt: "remote prompt"}, true }
func (f farSource) Checks() []capture.Check      { return f.checks }

func init() { noColor = true }

func TestCardRightColumnAlignsAcrossScripts(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.Local)
	const width = 60

	titles := []string{
		"WebSocket launch regression",
		"notes-api 搜索分页游标漂移排障",
		"SaaS 公共能力盘点补全",
		"这是一个非常非常非常非常非常非常长的中文标题需要被截断",
	}
	for _, title := range titles {
		r := &fav.Rec{
			Title: title, Provider: fav.ProviderClaude, Project: "notes-api",
			Status: fav.StatusDone, FavoritedAt: new(now.Add(-90 * time.Minute)),
		}
		got := Card(r, width, now)[0]
		if w := Width(got); w != width {
			t.Errorf("标题 %q 渲染出 %d 列，应为 %d：%q", title, w, width, got)
		}
		if !strings.HasSuffix(got, "16:30") {
			t.Errorf("标题 %q 的时间戳未能右对齐：%q", title, got)
		}
	}
}

func TestWrapNeverExceedsWidth(t *testing.T) {
	const width = 40
	texts := []string{
		"排查搜索接口游标分页重复返回同一条的问题。确认同秒更新的记录游标不唯一时，后端会回退到 embedded YAML default。",
		"一个没有任何空格的超长中文串用来验证逐字符断行是否生效不会超出宽度限制",
		"/Users/me/work/notes-api/internal/search/cursor.go",
	}
	for _, s := range texts {
		for _, line := range Wrap(s, width) {
			if w := Width(line); w > width {
				t.Errorf("折行结果 %d 列超过 %d：%q", w, width, line)
			}
		}
		joined := strings.Join(Wrap(s, width), "")
		if stripped := strings.ReplaceAll(strings.Join(strings.Fields(s), ""), " ", ""); !containsAllRunes(joined, stripped) {
			t.Errorf("折行丢字了：%q", s)
		}
	}
}

func containsAllRunes(got, want string) bool {
	g := strings.ReplaceAll(got, " ", "")
	return g == want
}

func TestTruncateAndPad(t *testing.T) {
	if got := Pad("排障", 10); Width(got) != 10 {
		t.Errorf("Pad 应补到 10 列，got %d", Width(got))
	}
	if got := PadLeft("16:42", 8); got != "   16:42" {
		t.Errorf("PadLeft got %q", got)
	}
	if got := Truncate("非常长的中文标题", 6); Width(got) > 6 {
		t.Errorf("Truncate 超宽：%q (%d 列)", got, Width(got))
	}
	if got := Truncate("短", 10); got != "短" {
		t.Errorf("未超宽时不该改动：%q", got)
	}
}

func TestLineIsSingleLineWithHiddenID(t *testing.T) {
	r := &fav.Rec{
		ID: "abc123", Title: "notes-api 排障", Provider: fav.ProviderClaude,
		Project: "notes-api", Tags: []string{"notes-api", "debug"},
		Status: fav.StatusDone, FavoritedAt: new(time.Now()),
	}
	got := Line(r, time.Now())
	if strings.Contains(got, "\n") {
		t.Fatal("FZF 候选必须是单行")
	}
	id, rest, ok := strings.Cut(got, Sep)
	if !ok || id != "abc123" {
		t.Fatalf("首字段应是 record id，got %q", id)
	}
	if strings.Contains(rest, Sep) {
		t.Fatal("显示部分不应再含制表符，否则 --with-nth=2 会截掉后半段")
	}
}

func TestWhen(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.Local) // a Saturday
	RelativeTime = false
	if got := When(time.Date(2026, 9, 12, 16, 42, 0, 0, time.Local), now); got != "2026-09-12 16:42" {
		t.Errorf("绝对模式 When = %q，年月日时分都要有", got)
	}
	RelativeTime = true
	for want, in := range map[string]time.Time{
		"16:42":      time.Date(2026, 9, 12, 16, 42, 0, 0, time.Local),
		"昨天 09:00":   time.Date(2026, 9, 11, 9, 0, 0, 0, time.Local),
		"周一 09:00":   time.Date(2026, 9, 7, 9, 0, 0, 0, time.Local),
		"09-05":      time.Date(2026, 9, 5, 9, 0, 0, 0, time.Local),
		"2025-12-01": time.Date(2025, 12, 1, 9, 0, 0, 0, time.Local),
	} {
		if got := When(in, now); got != want {
			t.Errorf("相对模式 When(%v) = %q，想要 %q", in, got, want)
		}
	}
	cases := map[string]time.Time{
		"今天 2026-09-12": time.Date(2026, 9, 12, 16, 42, 0, 0, time.Local),
		"昨天 2026-09-11": time.Date(2026, 9, 11, 9, 0, 0, 0, time.Local),
		"2026-09-01":    time.Date(2026, 9, 1, 9, 0, 0, 0, time.Local),
	}
	for want, in := range cases {
		if got := DayLabel(in, now); got != want {
			t.Errorf("DayLabel(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestPreviewReportsDeadTranscript(t *testing.T) {
	r := &fav.Rec{
		Title: "排障", Provider: fav.ProviderClaude, SessionID: "x",
		Status: fav.StatusDone, FavoritedAt: new(time.Now()),
		TranscriptPath: "/nope/definitely-missing.jsonl",
	}
	if !strings.Contains(Preview(r, ownFiles{r}, 60, time.Now()), "会话记录文件已失效") {
		t.Fatal("transcript 失效时 preview 必须提示")
	}
}

func TestRemoteRowCarriesItsHost(t *testing.T) {
	now := time.Now()
	r := &fav.Rec{ID: "abc", Provider: fav.ProviderClaude, SessionID: "0123-sid", Title: "远端", Project: "notes-api",
		Host: "mba", TranscriptPath: "/nope/definitely-missing.jsonl"}
	if k := LineKey(r); k != "mba:0123-sid" {
		t.Errorf("a remote row's key is host:sid, even when favorited there: %q", k)
	}
	if k := LineKey(&fav.Rec{ID: "abc", SessionID: "0123-sid"}); k != "abc" {
		t.Errorf("a local favorite keeps its record id: %q", k)
	}
	line := Line(r, now)
	if _, vis, _ := strings.Cut(line, Sep); !strings.Contains(vis, "@mba") {
		t.Errorf("the row shows the host: %q", line)
	}
	for _, l := range Card(r, 40, now) {
		if Width(l) > 40 {
			t.Errorf("card line wider than 40: %q", l)
		}
	}
	out := Preview(r, farSource{[]capture.Check{{OK: true, Text: "checked there"}}}, 60, now)
	for _, want := range []string{"@mba", "checked there", "remote prompt", "ssh mba"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "会话记录文件已失效") {
		t.Error("a remote row's checks come from its source, not this machine's files")
	}
}

func TestWrapFillsLineWithMixedText(t *testing.T) {
	const width = 66
	s := "鼠标：点标签页切视图、点 chip 起筛选、点卡片选中、双击恢复、滚轮翻页。"
	lines := Wrap(s, width)
	if len(lines) < 2 {
		t.Fatalf("这段文字应当折成两行，得到 %d 行", len(lines))
	}
	if w := Width(lines[0]); w < width-2 {
		t.Errorf("第一行只填了 %d 列，应当接近 %d：%q", w, width, lines[0])
	}
	if !strings.Contains(strings.Join(lines, ""), "chip") {
		t.Errorf("拉丁词被切碎了：%q", lines)
	}
}

func TestWrapKeepsPunctuationOffLineStarts(t *testing.T) {
	text := "搜所有会话的消息：按 >（》也行）打开搜索框并在前面填好 >；关键词在所有消息和命令里找，其余的筛选词（project: #标签 last: status:）限定会话范围；命令行：fav mv <旧> <新>，fav fix 会找已不存在的目录去了哪"
	for w := 12; w <= 60; w++ { // narrower, a mark plus its word may not fit: the width wins
		for _, l := range Wrap(text, w) {
			if Width(l) > w {
				t.Fatalf("width %d: %q is %d wide", w, l, Width(l))
			}
			if l == "" {
				continue
			}
			r, _ := utf8.DecodeRuneInString(l)
			if strings.ContainsRune("，。、；：！？）」』》", r) {
				t.Errorf("width %d: line starts with %q: %q", w, r, l)
			}
			lr, _ := utf8.DecodeLastRuneInString(l)
			if strings.ContainsRune("（「『《", lr) {
				t.Errorf("width %d: line ends with %q: %q", w, lr, l)
			}
		}
	}
}

func TestFileList(t *testing.T) {
	fs := []fav.FileCount{{Path: "/w/app/internal/a.go", N: 3}, {Path: "/other/b.md", N: 1}}
	if got := FileList(fs, "/w/app"); got != filepath.FromSlash("internal/a.go")+" ×3  ·  /other/b.md" {
		t.Errorf("relative under base, count after: %q", got)
	}
}
