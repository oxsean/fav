package fav

import (
	"testing"
	"time"
)

//go:fix inline
func ptr(t time.Time) *time.Time { return new(t) }

func rec(title string, mod func(*Rec)) *Rec {
	r := &Rec{
		ID: NewID(), Provider: ProviderClaude, Title: title,
		Status: StatusDone, FavoritedAt: new(time.Now()),
	}
	if mod != nil {
		mod(r)
	}
	r.buildHay()
	return r
}

func filter(q Query, recs ...*Rec) []string {
	var out []string
	for _, r := range recs {
		if q.Match(r) {
			out = append(out, r.Title)
		}
	}
	return out
}

func TestParseDocExamples(t *testing.T) {
	hit := rec("WebSocket launch regression", func(r *Rec) {
		r.Tags = []string{"notes-api", "debug", "websocket"}
		r.Project = "notes-api"
		r.Summary = "cursor drift 相关排查"
		r.FavoritedAt = new(time.Date(2026, 9, 12, 16, 42, 0, 0, time.Local))
	})
	wrongTag := rec("RBAC 数据范围设计", func(r *Rec) {
		r.Tags = []string{"rbac", "design"}
		r.Project = "webapp"
		r.Provider = ProviderCodex
		r.FavoritedAt = new(time.Date(2026, 9, 12, 9, 0, 0, 0, time.Local))
	})
	tooOld := rec("旧的 websocket 记录", func(r *Rec) {
		r.Tags = []string{"notes-api", "debug", "websocket"}
		r.Project = "notes-api"
		r.Summary = "cursor drift"
		r.FavoritedAt = new(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local))
	})

	t.Run("多标签 AND 加关键词", func(t *testing.T) {
		got := filter(Parse("#notes-api #debug websocket"), hit, wrongTag, tooOld)
		if len(got) != 2 {
			t.Fatalf("want 2 hits, got %v", got)
		}
	})

	t.Run("字段限定加时间加多关键词", func(t *testing.T) {
		q := Parse("project:notes-api provider:claude status:done after:2026-09-01 cursor drift")
		got := filter(q, hit, wrongTag, tooOld)
		if len(got) != 1 || got[0] != hit.Title {
			t.Fatalf("want only %q, got %v", hit.Title, got)
		}
	})

	t.Run("相对时间加已完成", func(t *testing.T) {
		done := rec("已完成的活儿", func(r *Rec) { r.Status = StatusDone })
		q := Parse("last:7d status:completed")
		got := filter(q, hit, done, tooOld)
		if len(got) != 1 || got[0] != done.Title {
			t.Fatalf("want only %q, got %v", done.Title, got)
		}
	})
}

func TestDefaultScopeIsActiveOnly(t *testing.T) {
	active := rec("进行中", func(r *Rec) { r.Status = StatusDoing })
	todo := rec("待办", func(r *Rec) { r.Status = StatusTodo })
	done := rec("已完成", nil)
	now := time.Now()
	archived := rec("已归档", func(r *Rec) { r.ArchivedAt = &now })
	archivedDone := rec("已归档的已完成", func(r *Rec) { r.Status = StatusDone; r.ArchivedAt = &now })
	unfav := rec("未收藏", func(r *Rec) { r.FavoritedAt = nil })

	all := []*Rec{active, todo, done, archived, archivedDone, unfav}
	got := filter(Parse(""), all...)
	if len(got) != 3 || got[2] != "已完成" {
		t.Fatalf("默认作用域应含未归档的全部，got %v", got)
	}
	for _, c := range []struct {
		q string
		n int
	}{{"status:active", 2}, {"status:open", 3}, {"status:archived", 2}, {"status:done", 1}, {"status:todo", 1}, {"status:doing", 1}, {"status:all", 5}} {
		if got := filter(Parse(c.q), all...); len(got) != c.n {
			t.Fatalf("%s got %v", c.q, got)
		}
	}
	q := Parse("status:all")
	q.All = true
	if got := filter(q, active, todo, done, archived, unfav); len(got) != 5 {
		t.Fatalf("All 作用域下 status:all 应全看到，got %v", got)
	}
}

func TestUnknownQualifierFallsBackToText(t *testing.T) {
	q := Parse("branch:feature/cursor-pagination")
	if len(q.Unknown) != 1 {
		t.Fatalf("应记下无法识别的限定词以便 UI 提示，got %v", q.Unknown)
	}
	hit := rec("排障", func(r *Rec) { r.GitBranch = "feature/cursor-pagination" })
	if q.Match(hit) {
		t.Fatal("无法识别的限定词应作为普通文本参与匹配，而不是静默变成 branch 过滤")
	}
	if !Parse("feature/cursor-pagination").Match(hit) {
		t.Fatal("分支名应能被普通关键词搜到")
	}
}

func TestChineseSubstring(t *testing.T) {
	r := rec("notes-api 搜索分页游标漂移排障", func(x *Rec) {
		x.Summary = "确认 region-specific toggle 未创建时后端回退到 embedded YAML default"
	})
	for _, kw := range []string{"排障", "notes-api", "回退", "游标"} {
		if !Parse(kw).Match(r) {
			t.Errorf("中文/英文子串 %q 应命中", kw)
		}
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.Local)
	for _, c := range []struct {
		in   string
		want time.Time
	}{
		{"2026-09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)},
		{"09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)},
		{"9-1", time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)},
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.AddDate(0, 0, -7)},
		{"2w", now.AddDate(0, 0, -14)},
	} {
		got, ok := ParseWhen(c.in, now)
		if !ok || !got.Equal(c.want) {
			t.Errorf("ParseWhen(%q) = %v, %v; want %v", c.in, got, ok, c.want)
		}
	}
	if _, ok := ParseWhen("下周", now); ok {
		t.Error("无法解析的时间应返回 false，而不是悄悄给个零值")
	}
}

func TestStatusAgentIsItsOwnListing(t *testing.T) {
	q := Parse("status:agent kol")
	if q.Status != StatusAgent || len(q.Words) != 1 {
		t.Fatalf("parse gave status=%q words=%v", q.Status, q.Words)
	}
	r := &Rec{Provider: ProviderClaude, SessionID: "x", Title: "kol-13 run", Cwd: "/tmp/kol-13"}
	r.Prepare()
	q.All = true
	if !q.Match(r) {
		t.Fatal("an agent row with a matching keyword should pass")
	}
	if Parse("status:agent").Match(&Rec{Provider: ProviderClaude, SessionID: "y"}) {
		t.Fatal("without All the listing stays empty (agent rows never come from the store)")
	}
}

func TestLastMeansActiveSince(t *testing.T) {
	started := time.Now().AddDate(0, 0, -10)
	r := &Rec{ID: "x", SessionStartedAt: &started, LastAt: time.Now().Add(-time.Hour), FavoritedAt: &started}
	if !Parse("last:1d").Match(r) {
		t.Error("started ten days ago, active an hour ago: last:1d finds it")
	}
	if Parse("after:" + time.Now().AddDate(0, 0, -1).Format("2006-01-02")).Match(r) {
		t.Error("after: still means when the session started")
	}
}

func TestUnmarkedSessionsAreNotActive(t *testing.T) {
	now := time.Now()
	r := &Rec{Provider: ProviderClaude, SessionID: "s", Turns: 9, SessionStartedAt: &now}
	q := Parse("status:active")
	q.All = true // the sessions view
	if q.Match(r) {
		t.Error("an unfavorited session nobody marked is not in status:active")
	}
	r.Status = StatusDoing
	if !q.Match(r) {
		t.Error("marked doing: status:active finds it")
	}
}

func TestFileQualifier(t *testing.T) {
	r := &Rec{ID: "1", FavoritedAt: new(time.Now()), Status: StatusDoing, Files: map[string]int{"/w/Internal/Index/scan.go": 3}}
	if !Parse("file:internal/index").Match(r) || Parse("file:cmd/").Match(r) {
		t.Error("file: matches a written path by case-insensitive substring")
	}
	if got := TopFiles(map[string]int{"/b": 1, "/a": 1, "/c": 5}, 2); len(got) != 2 || got[0].Path != "/c" || got[1].Path != "/a" {
		t.Errorf("most written first, then by path: %+v", got)
	}
}
