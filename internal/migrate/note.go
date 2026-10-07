package migrate

import (
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// FirstResume is the first message a resume of provider's session sid here starts with while the note of the
// migration that brought it is unread: read the note first. done records it sent (called once the resume is on its
// way). "" and a no-op when there is none.
func FirstResume(provider, sid string) (prompt string, done func()) {
	recs, _ := Of(provider, sid)
	for _, r := range recs {
		if r.State != StateDone {
			continue
		}
		if r.Role != RoleFrom || r.Note == "" || r.Noted != nil || !paths.Exists(r.Note) {
			break
		}
		return i18n.F("migrate.first_resume", r.Peer.Name, r.Note), func() {
			change(r.Migration, func(cur Record, ok bool) (*Record, error) {
				if !ok || cur.Noted != nil {
					return nil, nil
				}
				now := time.Now()
				cur.Noted = &now
				return &cur, nil
			})
		}
	}
	return "", func() {}
}

// Reminder is what resuming provider's session sid here says when its latest migration copied it away: going on
// with it on both machines forks it. "" when it did not.
func Reminder(provider, sid string) string {
	recs, _ := Of(provider, sid)
	for _, r := range recs {
		if r.State != StateDone {
			continue
		}
		if r.Role == RoleTo {
			return i18n.F("migrate.reminder", r.Peer.Name, render.WhenFull(r.At.Local()))
		}
		break
	}
	return ""
}
