package fulltext

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/filelock"
	"github.com/oxsean/fav/internal/tend"
)

func TestFindIsTheSearchOverTheRecordsTranscripts(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	one, spread, half := filepath.Join(src, "one.jsonl"), filepath.Join(src, "spread.jsonl"), filepath.Join(src, "half.jsonl")
	var lines []string
	for range 6 {
		lines = append(lines, claudeLine("user", "flyway baseline"))
	}
	writeTranscript(t, one, append(lines, claudeLine("user", "滚轮加速怎么调"))...)
	writeTranscript(t, spread, claudeLine("user", "滚轮太慢"), claudeLine("assistant", "加上加速就好"))
	writeTranscript(t, half, claudeLine("user", "只有滚轮"))
	ctx := context.Background()
	Update(ctx, dir, []string{one, spread, half}, Options{}, nil)

	recs := []*tend.Rec{
		{Provider: tend.ProviderClaude, SessionID: "half", TranscriptPath: half},
		{Provider: tend.ProviderClaude, SessionID: "spread"},
		{Provider: tend.ProviderClaude, SessionID: "one", TranscriptPath: one, Title: "滚轮"},
	}
	bySession := map[string][]string{recs[1].Key(): {spread}}
	got := Find(ctx, dir, recs, bySession, "滚轮 加速")
	want := Search(ctx, dir, Cands(recs, bySession), "滚轮 加速")
	unscored := func(rs []Result) []Result { // the recency weight moves with the clock
		out := slices.Clone(rs)
		for i := range out {
			out[i].Score = 0
		}
		return out
	}
	if len(got.Results) != 2 || !reflect.DeepEqual(unscored(got.Results), unscored(want)) {
		t.Fatalf("Find ranks as Search over the records' transcripts:\n%+v\n%+v", got.Results, want)
	}
	if got.Results[0].Cand != 2 || got.Results[1].Cand != 1 || got.TooLong || len(got.Fixes) != 0 {
		t.Fatalf("%+v", got)
	}

	if f := Find(ctx, dir, recs, bySession, "flyawy"); !slices.Equal(f.Fixes, []string{"flyway"}) || len(f.Results) != 1 || f.Results[0].Cand != 2 {
		t.Fatalf("a typo searches the known word and says so: %+v", f)
	}
	var many []string
	for i := range 65 {
		many = append(many, "w"+strconv.Itoa(i))
	}
	if f := Find(ctx, dir, recs, bySession, strings.Join(many, " ")); !f.TooLong || f.Results != nil {
		t.Fatalf("too many keywords: nothing searched: %+v", f)
	}
}

// TestABuilderWaitsUpToItsBudgetAndBuildsOnInTheBackground: an update the budget cuts short goes on; a call meanwhile
// waits for it rather than starting another (which would find the store locked), then catches up on what changed.
func TestABuilderWaitsUpToItsBudgetAndBuildsOnInTheBackground(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	src := t.TempDir()
	a, b := filepath.Join(src, "a.jsonl"), filepath.Join(src, "b.jsonl")
	writeTranscript(t, a, claudeLine("user", "alpha words"))
	writeTranscript(t, b, claudeLine("user", "beta words"))
	release, reached := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	runs := 0
	bl := &Builder{update: func(ctx context.Context, dir string, paths []string, opt Options, progress func(Progress)) (Progress, error) {
		mu.Lock()
		runs++
		first := runs == 1
		mu.Unlock()
		return Update(ctx, dir, paths, opt, func(p Progress) {
			progress(p)
			if first && p.Done == 1 {
				close(reached)
				<-release
			}
		})
	}}
	ctx := context.Background()
	building, busy := bl.Build(ctx, 20*time.Millisecond, []string{a, b}, nil, 0)
	if building == nil || building.Total > 2 || building.Done > 1 || busy {
		t.Fatalf("the budget ends first: %+v %v", building, busy)
	}
	<-reached
	if building, busy := bl.Build(ctx, time.Millisecond, []string{a, b}, nil, 0); building == nil || *building != (Progress{1, 2}) || busy {
		t.Fatalf("a call while the update runs waits for that one: %v %v", building, busy)
	}
	close(release)

	appendTranscript(t, a, claudeLine("user", "gamma words"))
	if building, busy := bl.Build(ctx, time.Minute, []string{a, b}, nil, 0); building != nil || busy {
		t.Fatalf("done within a long budget: %+v %v", building, busy)
	}
	mu.Lock()
	if runs != 2 {
		t.Errorf("the update started before the call is followed by one more: %d runs", runs)
	}
	mu.Unlock()
	recs := []*tend.Rec{{Provider: tend.ProviderClaude, SessionID: "a", TranscriptPath: a}, {Provider: tend.ProviderClaude, SessionID: "b", TranscriptPath: b}}
	if f := Find(ctx, Dir(), recs, nil, "words"); len(f.Results) != 2 {
		t.Fatalf("both transcripts are in the store: %+v", f)
	}
	if f := Find(ctx, Dir(), recs, nil, "gamma"); len(f.Results) != 1 {
		t.Fatalf("what grew before the call is in too: %+v", f)
	}

	unlock, err := filelock.TryLock(filepath.Join(Dir(), ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if building, busy := bl.Build(ctx, time.Minute, []string{a, b}, nil, 0); building != nil || !busy {
		t.Fatalf("another process holds the store: %+v %v", building, busy)
	}
	if building, busy := bl.Build(ctx, time.Minute, nil, nil, 0); building != nil || busy {
		t.Fatalf("no index yet: nothing to build: %+v %v", building, busy)
	}
}
