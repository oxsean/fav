package remote

import "time"

const (
	// GrepBudget is the time a node searches before it answers with what it found; within GrepWait.
	GrepBudget = 3 * time.Second
	// GrepWait bounds one machine's answer to a search across machines: a slow one is told as timed out, the others
	// answer meanwhile.
	GrepWait = 5 * time.Second
	// GrepLimit is how many sessions a client asks of each machine in a search across machines; GrepMax is the most a
	// node sends.
	GrepLimit = 100
	GrepMax   = maxQueryLimit
)

// Interleave merges machines' ranked hits: those matching every keyword in one message first, then the rest; in each
// tier every machine's first before any machine's second, since machines rank by their own corpus, which other
// machines' scores do not compare to. whole says whether a hit matched every keyword in one message; limit < 1 takes
// every hit.
func Interleave[T any](lists [][]T, whole func(T) bool, limit int) []T {
	out := []T{}
	for _, tier := range []bool{true, false} {
		var tiers [][]T
		for _, l := range lists {
			var in []T
			for _, h := range l {
				if whole(h) == tier {
					in = append(in, h)
				}
			}
			tiers = append(tiers, in)
		}
		for rank := 0; ; rank++ {
			took := false
			for _, in := range tiers {
				if rank < len(in) {
					if limit > 0 && len(out) == limit {
						return out
					}
					out, took = append(out, in[rank]), true
				}
			}
			if !took {
				break
			}
		}
	}
	return out
}
