package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// KindWebPush is a browser's Web Push subscription.
const KindWebPush = "webpush"

// PushDevice is somewhere a user's notices show: Target says where, sealed by the server.
type PushDevice struct {
	ID       string      `json:"id"`
	User     string      `json:"user"`
	Kind     string      `json:"kind"`
	Name     string      `json:"name,omitempty"`
	Target   []byte      `json:"-"`
	Created  time.Time   `json:"created,omitzero"`
	Renewed  time.Time   `json:"renewed,omitzero"`
	LastOK   time.Time   `json:"last_ok,omitzero"`
	Failures int         `json:"failures,omitempty"`
	Prefs    DevicePrefs `json:"prefs"`
	// Version is its registration: another owner makes another.
	Version int `json:"-"`
	// Credential is the session or token that registered or last renewed it: it pushes only while that is live.
	Credential string `json:"-"`
}

// ⚠️ The events a device may take: what waits on its user (the default) and a task of theirs done.
const (
	EventWaiting = "task.needs_you"
	EventDone    = "task.done"
)

// DevicePrefs is what a device wants of the notices; the zero value is the defaults. Events nil is EventWaiting
// alone; Hide leaves a push saying only how many things wait; Wait is how long, in seconds, a push waits for the page:
// 0 the default (30 s for a permission, 60 s else), -1 none.
type DevicePrefs struct {
	Events []string `json:"events"` // null: the default; [] none
	Hide   bool     `json:"hide,omitempty"`
	Wait   int      `json:"wait,omitempty"`
}

// Wants: the device takes pushes of event.
func (p DevicePrefs) Wants(event string) bool {
	if p.Events == nil {
		return event == EventWaiting
	}
	return slices.Contains(p.Events, event)
}

// Check: p names only events a device takes, each once, and a wait of -1, 0 or 1 s to an hour.
func (p DevicePrefs) Check() error {
	for i, e := range p.Events {
		if e != EventWaiting && e != EventDone || slices.Contains(p.Events[:i], e) {
			return errors.New("events")
		}
	}
	if p.Wait < -1 || p.Wait > 3600 {
		return errors.New("wait")
	}
	return nil
}

// Delivery states.
const (
	DeliveryPending  = "pending"
	DeliverySending  = "sending" // claimed by a try on its way
	DeliveryOK       = "ok"
	DeliveryGone     = "gone"     // its device went away
	DeliveryFailed   = "failed"   // the last try failed, or the channel refused it for good
	DeliveryCanceled = "canceled" // what it was about is gone, or its recipient may no longer see it
)

// Delivery is one notice to one recipient on one device ("" is the recipient's webhook): the outbox's row.
type Delivery struct {
	ID            int64
	Seq           int64
	User          string
	Event         string
	Device        string
	DeviceVersion int // the registration of Device it is for
	Status        string
	Attempts      int
	Next          time.Time
	Notice        []byte
	Result        string
	At            time.Time
}

// MaxDevices is how many push devices one person keeps.
const MaxDevices = 10

// ErrTooMany: the person already keeps MaxDevices devices.
var ErrTooMany = errors.New("too many devices")

// KeepDevice registers d, or renews the device already registered under hash (a browser's endpoint): it moves to
// d.User with d's target and name, renewed at now. What was still to go to someone else on it is canceled. A browser
// new to someone keeping MaxDevices already is ErrTooMany.
func (t *Team) KeepDevice(d PushDevice, hash string, now time.Time) (PushDevice, error) {
	var id string
	err := inTx(t.w, func(tx *sql.Tx) error {
		var others int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM push_devices WHERE user_id = ? AND target_hash != ?`, d.User, hash).Scan(&others); err != nil {
			return err
		}
		var mine int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM push_devices WHERE user_id = ? AND target_hash = ?`, d.User, hash).Scan(&mine); err != nil {
			return err
		}
		if mine == 0 && others >= MaxDevices {
			return ErrTooMany
		}
		_, err := tx.Exec(`INSERT INTO push_devices (id, user_id, kind, target, target_hash, name, created_at, renewed_at, credential_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (target_hash) DO UPDATE SET user_id = excluded.user_id, kind = excluded.kind, target = excluded.target,
				name = excluded.name, renewed_at = excluded.renewed_at, credential_id = excluded.credential_id,
				version = version + (user_id != excluded.user_id)`,
			newID("d_"), d.User, d.Kind, d.Target, hash, d.Name, now.UnixNano(), now.UnixNano(), d.Credential)
		if err != nil {
			return err
		}
		var version int
		if err := tx.QueryRow(`SELECT id, version FROM push_devices WHERE target_hash = ?`, hash).Scan(&id, &version); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE deliveries SET status = ?, result = ? WHERE device_id = ? AND user_id != ? AND status IN (?, ?)`,
			DeliveryCanceled, "another owner", id, d.User, DeliveryPending, DeliverySending); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE deliveries SET device_version = ? WHERE device_id = ? AND user_id = ? AND status = ?`, version, id, d.User, DeliveryPending)
		return err
	})
	if err != nil {
		return PushDevice{}, err
	}
	kept, _, err := t.device(t.w, id)
	return kept, err
}

const deviceCols = `id, user_id, kind, target, name, created_at, renewed_at, last_ok_at, failures, prefs, credential_id, version`

func scanDevice(s interface{ Scan(...any) error }) (PushDevice, error) {
	var d PushDevice
	var created, renewed, ok int64
	var prefs string
	err := s.Scan(&d.ID, &d.User, &d.Kind, &d.Target, &d.Name, &created, &renewed, &ok, &d.Failures, &prefs, &d.Credential, &d.Version)
	d.Created, d.Renewed, d.LastOK = fromNanos(created), fromNanos(renewed), fromNanos(ok)
	json.Unmarshal([]byte(prefs), &d.Prefs) // one it cannot read is the defaults
	return d, err
}

// SetDevicePrefs keeps p as what user's device id wants; false when user has no such device.
func (t *Team) SetDevicePrefs(user, id string, p DevicePrefs) (bool, error) {
	b, _ := json.Marshal(p)
	res, err := t.w.Exec(`UPDATE push_devices SET prefs = ? WHERE id = ? AND user_id = ?`, string(b), id, user)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RemoveUserDevice removes user's device id, as they asked; false when they have no such device.
func (t *Team) RemoveUserDevice(user, id string) (bool, error) {
	var owner string
	err := t.w.QueryRow(`SELECT user_id FROM push_devices WHERE id = ?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != user {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, t.RemoveDevice(id, "removed")
}

func (t *Team) device(db *sql.DB, id string) (PushDevice, bool, error) {
	d, err := scanDevice(db.QueryRow(`SELECT `+deviceCols+` FROM push_devices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PushDevice{}, false, nil
	}
	return d, err == nil, err
}

// Device is the device id, false when there is none.
func (t *Team) Device(id string) (PushDevice, bool, error) { return t.device(t.r, id) }

// Devices are user's devices, oldest first.
func (t *Team) Devices(user string) ([]PushDevice, error) {
	rows, err := t.r.Query(`SELECT `+deviceCols+` FROM push_devices WHERE user_id = ? ORDER BY created_at, id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushDevice
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceCredentials is each device's id and the credential it pushes through.
func (t *Team) DeviceCredentials() (map[string]string, error) {
	rows, err := t.r.Query(`SELECT id, credential_id FROM push_devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, cred string
		if err := rows.Scan(&id, &cred); err != nil {
			return nil, err
		}
		out[id] = cred
	}
	return out, rows.Err()
}

// DropDevice removes user's device registered under hash, with the deliveries still to go to it.
func (t *Team) DropDevice(user, hash string) error {
	var id string
	err := t.w.QueryRow(`SELECT id FROM push_devices WHERE user_id = ? AND target_hash = ?`, user, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return t.RemoveDevice(id, "dropped")
}

// RemoveDevice removes device id, which went away (why: the channel's answer); what was still to go to it is gone.
func (t *Team) RemoveDevice(id, why string) error {
	return inTx(t.w, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM push_devices WHERE id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE deliveries SET status = ?, result = ? WHERE device_id = ? AND status IN (?, ?)`,
			DeliveryGone, why, id, DeliveryPending, DeliverySending)
		return err
	})
}

// RemoveGone removes device id when it is still at registration version: its push service said it went (why).
func (t *Team) RemoveGone(id string, version int, why string) error {
	var now int
	err := t.w.QueryRow(`SELECT version FROM push_devices WHERE id = ?`, id).Scan(&now)
	if errors.Is(err, sql.ErrNoRows) || err == nil && now != version {
		return nil
	}
	if err != nil {
		return err
	}
	return inTx(t.w, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM push_devices WHERE id = ? AND version = ?`, id, version)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		_, err = tx.Exec(`UPDATE deliveries SET status = ?, result = ? WHERE device_id = ? AND status IN (?, ?)`,
			DeliveryGone, why, id, DeliveryPending, DeliverySending)
		return err
	})
}

// DeviceWorked records that a delivery to device id at registration version went through at at.
func (t *Team) DeviceWorked(id string, version int, at time.Time) error {
	_, err := t.w.Exec(`UPDATE push_devices SET last_ok_at = ?, failures = 0 WHERE id = ? AND version = ?`, at.UnixNano(), id, version)
	return err
}

// DeviceFailed counts a failed delivery to device id at registration version.
func (t *Team) DeviceFailed(id string, version int) error {
	_, err := t.w.Exec(`UPDATE push_devices SET failures = failures + 1 WHERE id = ? AND version = ?`, id, version)
	return err
}

// ExpireDevices removes the devices last renewed before before, and answers how many went.
func (t *Team) ExpireDevices(before time.Time) (int, error) {
	rows, err := t.w.Query(`SELECT id FROM push_devices WHERE renewed_at < ?`, before.UnixNano())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := t.RemoveDevice(id, "expired"); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// Enqueue puts ds in the outbox as pending, in one transaction; a delivery of the journal (Seq > 0) already there is
// left as it is. It answers how many went in.
func (t *Team) Enqueue(ds []Delivery) (int, error) {
	n := 0
	err := inTx(t.w, func(tx *sql.Tx) error {
		for _, d := range ds {
			res, err := tx.Exec(`INSERT OR IGNORE INTO deliveries (seq, user_id, event, device_id, device_version, status, next_at, notice, at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.Seq, d.User, d.Event, d.Device, d.DeviceVersion, DeliveryPending, d.Next.UnixNano(), string(d.Notice), d.At.UnixNano())
			if err != nil {
				return err
			}
			k, _ := res.RowsAffected()
			n += int(k)
		}
		return nil
	})
	return n, err
}

const deliveryCols = `id, seq, user_id, event, device_id, device_version, status, attempts, next_at, notice, result, at`

// Due are up to n pending deliveries whose time has come by now, soonest first.
func (t *Team) Due(now time.Time, n int) ([]Delivery, error) {
	return t.deliveries(`SELECT `+deliveryCols+` FROM deliveries WHERE status = ? AND next_at <= ? ORDER BY next_at, id LIMIT ?`,
		DeliveryPending, now.UnixNano(), n)
}

// deliveryKey is the SQL for what a delivery takes its turn by: its device, or for a webhook its recipient ("hook:"
// and their id).
const deliveryKey = `CASE WHEN device_id = '' THEN 'hook:' || user_id ELSE device_id END`

// Batch is what a channel starts next: the soonest delivery due by now of each device (webhooks: of each recipient's
// webhook) whose key is not busy, up to n, soonest first.
func (t *Team) Batch(now time.Time, webhooks bool, n int, busy []string) ([]Delivery, error) {
	b, _ := json.Marshal(append([]string{}, busy...))
	return t.deliveries(`SELECT `+deliveryCols+` FROM (SELECT *, ROW_NUMBER() OVER (PARTITION BY `+deliveryKey+` ORDER BY next_at, id) AS turn
		FROM deliveries WHERE status = ? AND next_at <= ? AND (device_id = '') = ? AND `+deliveryKey+` NOT IN (SELECT value FROM json_each(?)))
		WHERE turn = 1 ORDER BY next_at, id LIMIT ?`, DeliveryPending, now.UnixNano(), webhooks, string(b), n)
}

func (t *Team) deliveries(query string, args ...any) ([]Delivery, error) {
	rows, err := t.r.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var next, at int64
		var notice string
		if err := rows.Scan(&d.ID, &d.Seq, &d.User, &d.Event, &d.Device, &d.DeviceVersion, &d.Status, &d.Attempts, &next, &notice, &d.Result, &at); err != nil {
			return nil, err
		}
		d.Next, d.At, d.Notice = fromNanos(next), fromNanos(at), []byte(notice)
		out = append(out, d)
	}
	return out, rows.Err()
}

// PruneDeliveries removes the deliveries that ended (settled or canceled) of notices before before, and answers how
// many went.
func (t *Team) PruneDeliveries(before time.Time) (int, error) {
	res, err := t.w.Exec(`DELETE FROM deliveries WHERE status NOT IN (?, ?) AND at < ?`, DeliveryPending, DeliverySending, before.UnixNano())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// NextDue is when the soonest pending delivery is due; false when none is pending.
func (t *Team) NextDue() (time.Time, bool, error) {
	var next sql.NullInt64
	if err := t.r.QueryRow(`SELECT MIN(next_at) FROM deliveries WHERE status = ?`, DeliveryPending).Scan(&next); err != nil || !next.Valid {
		return time.Time{}, false, err
	}
	return time.Unix(0, next.Int64).UTC(), true, nil
}

// Claim takes pending delivery id for a try, while its device is still the registration it was written for: false
// when it is not pending, or the device changed hands or went.
func (t *Team) Claim(id int64) (bool, error) {
	res, err := t.w.Exec(`UPDATE deliveries SET status = ? WHERE id = ? AND status = ? AND (device_id = '' OR EXISTS (
		SELECT 1 FROM push_devices p WHERE p.id = deliveries.device_id AND p.version = deliveries.device_version AND p.user_id = deliveries.user_id))`,
		DeliverySending, id, DeliveryPending)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Unclaim puts back to pending what tries on their way held when the server stopped.
func (t *Team) Unclaim() error {
	_, err := t.w.Exec(`UPDATE deliveries SET status = ? WHERE status = ?`, DeliveryPending, DeliverySending)
	return err
}

// Settle records how delivery id went, unless it was settled or canceled meanwhile: its state, the channel's answer,
// the tries so far, and, while it is still pending, when it goes next.
func (t *Team) Settle(id int64, status, result string, attempts int, next time.Time) error {
	_, err := t.w.Exec(`UPDATE deliveries SET status = ?, result = ?, attempts = ?, next_at = ? WHERE id = ? AND status IN (?, ?)`,
		status, result, attempts, nanos(next), id, DeliveryPending, DeliverySending)
	return err
}
