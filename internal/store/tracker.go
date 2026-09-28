package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Tracker is a project's binding to one repository of an issue tracker, and how its sync stands. Token and
// HookSecret are sealed by the caller; the store never sees them in the clear.
type Tracker struct {
	ID         string
	Project    string
	Kind       string
	Base       string
	Repo       string
	RepoID     int64
	Bot        string // the login the token acts as
	Token      []byte
	HookSecret []byte
	Settings   string // JSON
	CreatedBy  string
	Created    time.Time
	Cursor     time.Time // issues updated before it were read
	ETag       string
	Polled     time.Time
	Paused     time.Time // a rate limit: nothing is sent before it
	Stopped    string    // why syncing stopped until the credential is replaced ("" while it runs)
	LastOK     time.Time
	LastError  string
}

// TrackerIssue is how one issue's write-back stands.
type TrackerIssue struct {
	Tracker   string
	Number    int64
	Task      string
	CommentID int64
	BodyHash  string
	Written   time.Time // when the progress comment was last written
	Closed    bool      // tend closed (or labelled) it after acceptance
	Dirty     bool      // to be read again
	LastError string
	Parent    int64     // a sub-issue tend made for a subtask of that issue's task; never a requirement
	PR        string    // the pull or merge request tend opened from its task's branch
	Synced    time.Time // when tend last brought the issue and its task in step
}

const trackerCols = `id, project, kind, base, repo, repo_id, bot, token, hook_secret, settings, created_by, created, cursor, etag,
	polled, paused_until, stopped, last_ok, last_error`

func scanTracker(row interface{ Scan(...any) error }) (Tracker, error) {
	var x Tracker
	var created, cursor, polled, paused, ok int64
	err := row.Scan(&x.ID, &x.Project, &x.Kind, &x.Base, &x.Repo, &x.RepoID, &x.Bot, &x.Token, &x.HookSecret, &x.Settings, &x.CreatedBy,
		&created, &cursor, &x.ETag, &polled, &paused, &x.Stopped, &ok, &x.LastError)
	x.Created, x.Cursor, x.Polled, x.Paused, x.LastOK = fromNanos(created), fromNanos(cursor), fromNanos(polled), fromNanos(paused), fromNanos(ok)
	return x, err
}

// AddTracker binds x (its ID is made here); ErrExists when that repository is bound already.
func (t *Team) AddTracker(x Tracker) (Tracker, error) {
	x.ID, x.Created = newID("tr_"), time.Now().UTC()
	_, err := t.w.Exec(`INSERT INTO trackers (id, project, kind, base, repo, repo_id, bot, token, hook_secret, settings, created_by, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, x.ID, x.Project, x.Kind, x.Base, x.Repo, x.RepoID, x.Bot, x.Token, x.HookSecret,
		x.Settings, x.CreatedBy, x.Created.UnixNano())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return Tracker{}, ErrExists
	}
	return x, err
}

func (t *Team) Trackers() ([]Tracker, error) {
	rows, err := t.r.Query(`SELECT ` + trackerCols + ` FROM trackers ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tracker
	for rows.Next() {
		x, err := scanTracker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (t *Team) Tracker(id string) (Tracker, error) {
	x, err := scanTracker(t.r.QueryRow(`SELECT `+trackerCols+` FROM trackers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Tracker{}, ErrNotFound
	}
	return x, err
}

// RemoveTracker unbinds id and forgets its issues' write-back.
func (t *Team) RemoveTracker(id string) error {
	return affected(t.w.Exec(`DELETE FROM trackers WHERE id = ?`, id))
}

// SetTrackerCredential replaces id's token, which acts as bot, and lets it sync again.
func (t *Team) SetTrackerCredential(id, bot string, token []byte) error {
	return affected(t.w.Exec(`UPDATE trackers SET bot = ?, token = ?, stopped = '', paused_until = 0, last_error = '' WHERE id = ?`, bot, token, id))
}

// SetTrackerSettings replaces id's settings.
func (t *Team) SetTrackerSettings(id, settings string) error {
	return affected(t.w.Exec(`UPDATE trackers SET settings = ? WHERE id = ?`, settings, id))
}

// Polled records a finished scan of id: issues updated before cursor were read, etag names what was seen.
func (t *Team) Polled(id string, cursor time.Time, etag string, at time.Time) error {
	return affected(t.w.Exec(`UPDATE trackers SET cursor = ?, etag = ?, polled = ? WHERE id = ?`, nanos(cursor), etag, nanos(at), id))
}

// TrackerResult records how id's last exchange went: ok, a rate limit until paused, or a stop.
func (t *Team) TrackerResult(id string, ok time.Time, paused time.Time, stopped, lastErr string) error {
	if ok.IsZero() {
		return affected(t.w.Exec(`UPDATE trackers SET paused_until = ?, stopped = ?, last_error = ? WHERE id = ?`, nanos(paused), stopped, lastErr, id))
	}
	return affected(t.w.Exec(`UPDATE trackers SET last_ok = ?, paused_until = 0, stopped = '', last_error = '' WHERE id = ?`, nanos(ok), id))
}

// Rescan makes id read every issue again on its next scan.
func (t *Team) Rescan(id string) error {
	return affected(t.w.Exec(`UPDATE trackers SET cursor = 0, etag = '', polled = 0 WHERE id = ?`, id))
}

const issueCols = `tracker, number, task, comment_id, body_hash, written, closed, dirty, last_error, parent, pr, synced`

func scanIssue(row interface{ Scan(...any) error }) (TrackerIssue, error) {
	var x TrackerIssue
	var written, synced int64
	err := row.Scan(&x.Tracker, &x.Number, &x.Task, &x.CommentID, &x.BodyHash, &written, &x.Closed, &x.Dirty, &x.LastError, &x.Parent, &x.PR, &synced)
	x.Written, x.Synced = fromNanos(written), fromNanos(synced)
	return x, err
}

// TrackerIssue is how issue number of tracker stands; a zero one (with the keys filled in) when nothing is known.
func (t *Team) TrackerIssue(tracker string, number int64) (TrackerIssue, error) {
	x, err := scanIssue(t.r.QueryRow(`SELECT `+issueCols+` FROM tracker_issues WHERE tracker = ? AND number = ?`, tracker, number))
	if errors.Is(err, sql.ErrNoRows) {
		return TrackerIssue{Tracker: tracker, Number: number}, nil
	}
	return x, err
}

// PutTrackerIssue records x.
func (t *Team) PutTrackerIssue(x TrackerIssue) error {
	_, err := t.w.Exec(`INSERT INTO tracker_issues (`+issueCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tracker, number) DO UPDATE SET task = excluded.task, comment_id = excluded.comment_id, body_hash = excluded.body_hash,
		written = excluded.written, closed = excluded.closed, dirty = excluded.dirty, last_error = excluded.last_error,
		parent = excluded.parent, pr = excluded.pr, synced = excluded.synced`,
		x.Tracker, x.Number, x.Task, x.CommentID, x.BodyHash, nanos(x.Written), x.Closed, x.Dirty, x.LastError, x.Parent, x.PR, nanos(x.Synced))
	return err
}

// MarkDirty has issue number of tracker read again.
func (t *Team) MarkDirty(tracker string, number int64) error {
	_, err := t.w.Exec(`INSERT INTO tracker_issues (tracker, number, dirty) VALUES (?, ?, 1)
		ON CONFLICT (tracker, number) DO UPDATE SET dirty = 1`, tracker, number)
	return err
}

// TrackerIssues are tracker's issues, those to read again only when dirty.
func (t *Team) TrackerIssues(tracker string, dirty bool) ([]TrackerIssue, error) {
	q := `SELECT ` + issueCols + ` FROM tracker_issues WHERE tracker = ?`
	if dirty {
		q += ` AND dirty = 1`
	}
	rows, err := t.r.Query(q+` ORDER BY number`, tracker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackerIssue
	for rows.Next() {
		x, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// TakeDelivery takes webhook delivery id: false when it was taken before.
func (t *Team) TakeDelivery(id string) (bool, error) {
	res, err := t.w.Exec(`INSERT OR IGNORE INTO tracker_deliveries (id, at) VALUES (?, ?)`, id, time.Now().UnixNano())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UserByLogin is the member who signed in at issuer as username, "" when none (or more than one) did.
func (t *Team) UserByLogin(issuer, username string) (string, error) {
	rows, err := t.r.Query(`SELECT DISTINCT i.user_id FROM identities i JOIN users u ON u.id = i.user_id
		WHERE u.disabled = 0 AND lower(i.username) = lower(?) AND rtrim(i.issuer, '/') = rtrim(?, '/')`, username, issuer)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		return "", rows.Err()
	}
	return ids[0], rows.Err()
}

// LoginOf is the username user signed in with at issuer, "" when none.
func (t *Team) LoginOf(user, issuer string) (string, error) {
	var name string
	err := t.r.QueryRow(`SELECT username FROM identities WHERE user_id = ? AND rtrim(issuer, '/') = rtrim(?, '/') AND username != '' LIMIT 1`,
		user, issuer).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}
