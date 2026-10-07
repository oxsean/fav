package migrate

import "os"

// Changed is whether this side's main transcript moved on since migration rec: its size, or its sha, is not what rec
// recorded; nil when it cannot tell (the transcript is gone, moved away or unreadable).
func Changed(rec Record) *bool {
	p := mainTranscript(rec.SessionID)
	if p == "" || rec.SHA == "" {
		return nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	changed := st.Size() != rec.Size
	if !changed {
		sha, _, err := sumOf(p, st)
		if err != nil {
			return nil
		}
		changed = sha != rec.SHA
	}
	return &changed
}

// Relation is one of a session's migrations with whether this side moved on since (done ones only).
type Relation struct {
	Record
	Changed *bool
}

// Copies are provider's session sid's migrations this machine recorded, newest first; a done one says whether this
// side's transcript moved on since.
func Copies(provider, sid string) ([]Relation, error) {
	recs, err := Of(provider, sid)
	out := make([]Relation, len(recs))
	for i, r := range recs {
		out[i].Record = r
		if r.State == StateDone && !r.Moved {
			out[i].Changed = Changed(r)
		}
	}
	return out, err
}
