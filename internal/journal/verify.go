package journal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/oxsean/fav/internal/fileio"
)

// Report is what Verify found.
type Report struct {
	Path      string  `json:"path"`
	Size      int64   `json:"size"`
	Envelopes int     `json:"envelopes"` // good ones before any damage
	LastSeq   int64   `json:"last_seq"`
	Damage    *Damage `json:"damage,omitempty"`
	Torn      int64   `json:"torn,omitempty"` // bytes of a last line without its newline
}

// Damage is the first line that does not verify.
type Damage struct {
	Offset int64  `json:"offset"`
	Seq    int64  `json:"seq"` // the seq it should have had
	Reason string `json:"reason"`
	Last   bool   `json:"last"` // no whole line follows it
}

// Verify reads the log at path and checks every line's sum, that seqs follow without gaps, and, with fold, that each
// envelope applies; it writes nothing.
func Verify(path string, fold func(Envelope) error) (Report, error) {
	r := Report{Path: path}
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.Size = fi.Size()
	after := 0
	next, err := fileio.Lines(context.Background(), path, 0, maxLine, func(off int64, line []byte) bool {
		if r.Damage != nil {
			after++
			return true
		}
		env, err := parse(line)
		if err == nil && env.Seq != r.LastSeq+1 {
			err = fmt.Errorf("seq %d after %d", env.Seq, r.LastSeq)
		}
		if err == nil && fold != nil {
			err = fold(env)
		}
		if err != nil {
			r.Damage = &Damage{Offset: off, Seq: r.LastSeq + 1, Reason: err.Error()}
			return true
		}
		r.Envelopes++
		r.LastSeq = env.Seq
		return true
	})
	if err != nil {
		return r, err
	}
	if r.Damage != nil {
		r.Damage.Last = after == 0
	}
	r.Torn = r.Size - next
	return r, nil
}

// ErrUnsafe: the damage is not only at the end, so no cut can be proved harmless.
var ErrUnsafe = errors.New("damage before the last line")

// Repair cuts what Verify found at the end of the log: a torn tail, or a bad last line. It copies the whole log next
// to it first and answers the copy's path ("" when there was nothing to cut). The caller holds the coordinator lock.
func Repair(path string, r Report) (string, error) {
	cut := r.Size - r.Torn
	switch {
	case r.Damage != nil && !r.Damage.Last:
		return "", ErrUnsafe
	case r.Damage != nil:
		cut = r.Damage.Offset
	case r.Torn == 0:
		return "", nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.Size() != r.Size {
		return "", fmt.Errorf("%s changed since it was verified", path)
	}
	backup := fmt.Sprintf("%s.bak-%d", path, time.Now().Unix())
	if err := copyFile(path, backup); err != nil {
		return "", err
	}
	return backup, os.Truncate(path, cut)
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if err == nil {
		err = dst.Sync()
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(to)
	}
	return err
}
