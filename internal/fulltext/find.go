package fulltext

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/tend"
)

// Found is a message search over some sessions: Results index into the records searched.
type Found struct {
	Results []Result
	Fixes   []string // spellings searched besides the keywords (Expand)
	TooLong bool     // more keywords or terms than a search matches: nothing was searched
}

// Find searches the messages of recs, the sessions a query picked, for the keywords kw (Split): the sessions holding
// every one, ranked. bySession is the index's transcripts by session (index.PathsBySession); a record it lacks is
// searched in its own file. It returns nothing once ctx ends.
func Find(ctx context.Context, dir string, recs []*tend.Rec, bySession map[string][]string, kw string) Found {
	if TooLong(kw) {
		return Found{TooLong: true}
	}
	return Found{Results: Search(ctx, dir, Cands(recs, bySession), kw), Fixes: Expand(dir, ParseQuery(kw)).Fixes()}
}

// Builder keeps the store current for a process answering searches over a while (a node): one update at a time,
// waited for up to a budget. An update the budget cuts short goes on in the background, and later calls wait for it.
type Builder struct {
	mu     sync.Mutex
	run    *build
	update func(ctx context.Context, dir string, paths []string, opt Options, progress func(Progress)) (Progress, error)
}

type build struct {
	done chan struct{}
	prog Progress
	busy bool
}

// Build waits until the store is current for these transcripts (Sync's indexed and recs), budget passes or ctx ends.
// It starts an update unless one runs; one that started before this call is followed by another. building is how far
// the update got when it did not finish in time; busy: another process holds the store.
func (b *Builder) Build(ctx context.Context, budget time.Duration, indexed []string, recs []*tend.Rec, outLines int) (building *Progress, busy bool) {
	if len(indexed) == 0 { // ⚠️ the index is not built yet: an update would drop every text file
		return nil, false
	}
	dir, paths, opt := Dir(), sources(indexed, recs), Options{OutLines: outLines}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	wait := func(r *build) bool {
		select {
		case <-r.done:
			return true
		case <-timer.C:
		case <-ctx.Done():
		}
		return false
	}
	b.mu.Lock()
	r := b.run
	b.mu.Unlock()
	if r != nil && !wait(r) {
		return b.progress(r), false
	}
	b.mu.Lock()
	if r = b.run; r == nil {
		r = b.start(dir, paths, opt)
	}
	b.mu.Unlock()
	if !wait(r) {
		return b.progress(r), false
	}
	return nil, r.busy
}

// start runs an update in the background; the caller holds mu.
func (b *Builder) start(dir string, paths []string, opt Options) *build {
	r := &build{done: make(chan struct{})}
	b.run = r
	update := b.update
	if update == nil {
		update = Update
	}
	go func() {
		_, err := update(context.Background(), dir, paths, opt, func(p Progress) {
			b.mu.Lock()
			r.prog = p
			b.mu.Unlock()
		})
		b.mu.Lock()
		r.busy, b.run = errors.Is(err, ErrBusy), nil
		b.mu.Unlock()
		close(r.done)
	}()
	return r
}

func (b *Builder) progress(r *build) *Progress {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := r.prog
	return &p
}
