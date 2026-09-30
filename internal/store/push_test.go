package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func webhookRow(seq int64, user, event string, next time.Time) Delivery {
	return Delivery{Seq: seq, User: user, Event: event, Next: next, Notice: []byte(`{"task":"t1"}`), At: t0}
}

// before0011 takes a database back to before the devices' settings.
func before0011(t *testing.T, path string) {
	exec(t, path, `ALTER TABLE push_devices DROP COLUMN prefs; ALTER TABLE push_devices DROP COLUMN credential_id`)
}

// before0009 puts the tables 0009 changed back as they were before it.
func before0009(t *testing.T, path string) {
	exec(t, path, `DROP TABLE push_devices; DROP TABLE deliveries;
		CREATE TABLE deliveries (seq INTEGER NOT NULL, user_id TEXT NOT NULL, event TEXT NOT NULL, status TEXT NOT NULL DEFAULT '',
			at INTEGER NOT NULL, PRIMARY KEY (seq, user_id, event))`)
}

// A database from before the outbox keeps what it delivered, as done or failed, and sends none of it again; the new
// table takes one row per device beside the webhook's, and the server's own notices (seq 0) as often as they come.
func TestUpgradingTheDeliveriesKeepsWhatWentAndSendsNothingAgain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	tm, err := OpenTeam(path)
	if err != nil {
		t.Fatal(err)
	}
	tm.Close()
	before0015(t, path)
	before0014(t, path)
	before0013(t, path)
	before0012(t, path)
	before0011(t, path)
	before0010(t, path)
	before0009(t, path)
	exec(t, path, `INSERT INTO deliveries VALUES (3, 'local', 'task.needs_you', '200', 1), (4, 'local', 'task.done', '500', 2),
			(5, 'local', 'task.needs_you', '', 3), (6, 'u_a', 'task.needs_you', 'error', 4), (7, 'u_a', 'task.done', '204', 5)`)
	setVersion(t, path, 8)
	if tm, err = OpenTeam(path); err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	if baks, _ := filepath.Glob(filepath.Join(dir, File+".v8-*.bak")); len(baks) != 1 {
		t.Fatalf("no copy of the v8 database: %v", baks)
	}
	got := map[int64]string{}
	rows, err := tm.r.Query(`SELECT seq, status, result, device_id FROM deliveries`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq int64
		var status, result, device string
		must(t, rows.Scan(&seq, &status, &result, &device))
		got[seq] = status + " " + result + " " + device
	}
	rows.Close()
	want := map[int64]string{3: "ok 200 ", 4: "failed 500 ", 5: "failed  ", 6: "failed error ", 7: "ok 204 "}
	for seq, w := range want {
		if got[seq] != w {
			t.Errorf("seq %d: %q, want %q", seq, got[seq], w)
		}
	}
	if due, err := tm.Due(t0.Add(time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("the old deliveries went out again: %+v %v", due, err)
	}
	d := webhookRow(3, LocalUser, "task.needs_you", t0)
	d.Device = "d_1"
	own := webhookRow(0, LocalUser, "tracker.stopped", t0)
	if n, err := tm.Enqueue([]Delivery{d, own, own}); err != nil || n != 3 {
		t.Fatalf("a device's row beside the webhook's and the server's own notices twice: %d %v", n, err)
	}
	if n, _ := tm.Enqueue([]Delivery{d}); n != 0 {
		t.Fatal("the same delivery to the same device went in twice")
	}
}

// The outbox answers what is due, soonest first, and takes each delivery of the journal once.
func TestTheOutboxTakesADeliveryOnceAndAnswersWhatIsDue(t *testing.T) {
	tm := openTeam(t)
	later := webhookRow(9, LocalUser, "task.needs_you", t0.Add(time.Minute))
	later.Device = "d_1"
	now := webhookRow(9, LocalUser, "task.needs_you", t0)
	if n, err := tm.Enqueue([]Delivery{later, now}); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := tm.Enqueue([]Delivery{now}); n != 0 {
		t.Fatal("taken twice")
	}
	if next, ok, err := tm.NextDue(); err != nil || !ok || !next.Equal(t0) {
		t.Fatalf("next due %v %v %v", next, ok, err)
	}
	due, err := tm.Due(t0, 10)
	if err != nil || len(due) != 1 || due[0].Device != "" || string(due[0].Notice) != `{"task":"t1"}` || due[0].Status != DeliveryPending {
		t.Fatalf("due at t0: %+v %v", due, err)
	}
	must(t, tm.Settle(due[0].ID, DeliveryOK, "201", 1, time.Time{}))
	due, _ = tm.Due(t0.Add(time.Hour), 10)
	if len(due) != 1 || due[0].Device != "d_1" {
		t.Fatalf("after the first went: %+v", due)
	}
	must(t, tm.Settle(due[0].ID, DeliveryPending, "503", 1, t0.Add(2*time.Hour)))
	if due, _ = tm.Due(t0.Add(time.Hour), 10); len(due) != 0 {
		t.Fatalf("a retry came before its time: %+v", due)
	}
	if due, _ = tm.Due(t0.Add(2*time.Hour), 10); len(due) != 1 || due[0].Attempts != 1 || due[0].Result != "503" {
		t.Fatalf("the retry: %+v", due)
	}
	if next, ok, _ := tm.NextDue(); !ok || !next.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("next due %v", next)
	}
}

// A browser subscribing again is the same device, whoever signs in there now; a device nobody renewed for long goes,
// and a gone device takes its undelivered rows with it.
func TestADeviceIsFoundAgainByItsEndpoint(t *testing.T) {
	tm := openTeam(t)
	a, err := tm.KeepDevice(PushDevice{User: LocalUser, Kind: KindWebPush, Name: "Mac", Target: []byte("sealed-1"), Credential: "w_1"}, "h1", t0)
	if err != nil || a.ID == "" || a.Credential != "w_1" {
		t.Fatalf("%+v %v", a, err)
	}
	b, err := tm.KeepDevice(PushDevice{User: LocalUser, Kind: KindWebPush, Name: "Mac", Target: []byte("sealed-2"), Credential: "w_2"}, "h1", t0.Add(time.Hour))
	if err != nil || b.ID != a.ID || b.Credential != "w_2" {
		t.Fatalf("renewing made another device, or kept the old session: %+v %v", b, err)
	}
	if held, err := tm.DeviceCredentials(); err != nil || !reflect.DeepEqual(held, map[string]string{a.ID: "w_2"}) {
		t.Fatalf("%v %v", held, err)
	}
	if d, ok, _ := tm.Device(a.ID); !ok || string(d.Target) != "sealed-2" || !d.Renewed.Equal(t0.Add(time.Hour)) || !d.Created.Equal(t0) {
		t.Fatalf("after renewing: %+v", d)
	}
	c, _ := tm.KeepDevice(PushDevice{User: "u_b", Kind: KindWebPush, Name: "Android", Target: []byte("sealed-3")}, "h1", t0.Add(2*time.Hour))
	if c.ID != a.ID {
		t.Fatal("the browser became a second device")
	}
	if ds, _ := tm.Devices(LocalUser); len(ds) != 0 {
		t.Fatalf("the browser still pushes to whoever signed out there: %+v", ds)
	}
	if ds, _ := tm.Devices("u_b"); len(ds) != 1 || ds[0].Name != "Android" {
		t.Fatalf("%+v", ds)
	}
	must(t, tm.DropDevice(LocalUser, "h1"))
	if _, ok, _ := tm.Device(a.ID); !ok {
		t.Fatal("someone else dropped u_b's device")
	}

	must(t, tm.DeviceFailed(a.ID))
	must(t, tm.DeviceFailed(a.ID))
	if d, _, _ := tm.Device(a.ID); d.Failures != 2 {
		t.Fatalf("failures %d", d.Failures)
	}
	must(t, tm.DeviceWorked(a.ID, t0.Add(3*time.Hour)))
	if d, _, _ := tm.Device(a.ID); d.Failures != 0 || !d.LastOK.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("after it worked: %+v", d)
	}

	row := webhookRow(4, "u_b", "task.needs_you", t0)
	row.Device = a.ID
	other := row
	other.Device = ""
	tm.Enqueue([]Delivery{row, other})
	must(t, tm.RemoveDevice(a.ID, "410"))
	if _, ok, _ := tm.Device(a.ID); ok {
		t.Fatal("a gone device stayed")
	}
	due, _ := tm.Due(t0, 10)
	if len(due) != 1 || due[0].Device != "" {
		t.Fatalf("a gone device's row is still to go: %+v", due)
	}

	old, _ := tm.KeepDevice(PushDevice{User: "u_b", Kind: KindWebPush, Target: []byte("x")}, "h2", t0)
	fresh, _ := tm.KeepDevice(PushDevice{User: "u_b", Kind: KindWebPush, Target: []byte("y")}, "h3", t0.Add(80*24*time.Hour))
	if n, err := tm.ExpireDevices(t0.Add(10 * 24 * time.Hour)); err != nil || n != 1 {
		t.Fatalf("expired %d %v", n, err)
	}
	if _, ok, _ := tm.Device(old.ID); ok {
		t.Fatal("a device nobody renewed stayed")
	}
	if _, ok, _ := tm.Device(fresh.ID); !ok {
		t.Fatal("a renewed device went")
	}
}

// A browser someone else signs in on takes nothing of its last owner's still to go: those rows are canceled, while the
// same owner renewing keeps them.
func TestADeviceTakenOverDropsWhatWasStillToGoToItsLastOwner(t *testing.T) {
	tm := openTeam(t)
	a, _ := tm.KeepDevice(PushDevice{User: LocalUser, Kind: KindWebPush, Target: []byte("s")}, "h1", t0)
	mine := webhookRow(4, LocalUser, "task.needs_you", t0)
	mine.Device = a.ID
	hook := mine
	hook.Device = ""
	_, err := tm.Enqueue([]Delivery{mine, hook})
	must(t, err)
	tm.KeepDevice(PushDevice{User: LocalUser, Kind: KindWebPush, Target: []byte("s2")}, "h1", t0.Add(time.Minute))
	if due, _ := tm.Due(t0, 10); len(due) != 2 {
		t.Fatalf("renewed by its owner: %+v", due)
	}
	tm.KeepDevice(PushDevice{User: "u_b", Kind: KindWebPush, Target: []byte("s3")}, "h1", t0.Add(2*time.Minute))
	due, _ := tm.Due(t0, 10)
	if len(due) != 1 || due[0].Device != "" {
		t.Fatalf("still to go to the browser someone else signed in on: %+v", due)
	}
	var status string
	if err := tm.r.QueryRow(`SELECT status FROM deliveries WHERE device_id = ?`, a.ID).Scan(&status); err != nil || status != DeliveryCanceled {
		t.Fatalf("%q %v", status, err)
	}
}

// A device's notice settings start as the defaults, are its owner's alone to change or to remove the device by, and
// stay through its renewals; a database from before them gives every device the defaults.
// What a delivery recorded before 0011 names the address it went to, whose path is a push device's secret (or a
// webhook's): the upgrade leaves the error's class in its place and keeps every row as it was otherwise.
func TestUpgradingForgetsTheAddressesDeliveriesRecorded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	tm, err := OpenTeam(path)
	if err != nil {
		t.Fatal(err)
	}
	tm.Close()
	before0015(t, path)
	before0014(t, path)
	before0013(t, path)
	before0012(t, path)
	before0011(t, path)
	setVersion(t, path, 10)
	cut := `Post "https://fcm.googleapis.com/fcm/send/` + strings.Repeat("x", 200)
	was := map[int]string{
		1: `Post "https://fcm.googleapis.com/fcm/send/dQw4-s3cret:APA91b": dial tcp 142.250.74.10:443: connect: connection refused`,
		2: `Post "https://updates.push.services.mozilla.com/wpush/v2/gAAAAs3cret": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
		3: `Post "https://ntfy.example/s3cret-topic": dial tcp: lookup ntfy.example: no such host`,
		4: cut[:200],
		5: "503",
		6: "ok",
		7: "another owner",
	}
	for id, r := range was {
		exec(t, path, `INSERT INTO deliveries (id, seq, user_id, event, device_id, status, attempts, next_at, notice, result, at)
			VALUES (`+strconv.Itoa(id)+`, `+strconv.Itoa(id)+`, 'local', 'task.needs_you', 'd_`+strconv.Itoa(id)+`', 'failed', 4, 0, '{}', '`+strings.ReplaceAll(r, "'", "''")+`', 1)`)
	}
	if tm, err = OpenTeam(path); err != nil {
		t.Fatal(err)
	}
	tm.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, status, attempts, result FROM deliveries ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int]string{}
	for rows.Next() {
		var id, attempts int
		var status, result string
		must(t, rows.Scan(&id, &status, &attempts, &result))
		if status != "failed" || attempts != 4 {
			t.Fatalf("row %d changed: %s %d", id, status, attempts)
		}
		got[id] = result
	}
	want := map[int]string{1: "dial: connect: connection refused", 2: "Post: timeout", 3: "dial: no such host", 4: "Post: unreachable",
		5: "503", 6: "ok", 7: "another owner"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestADevicesSettingsAreItsOwnersAndOutliveItsRenewals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	tm, err := OpenTeam(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := tm.KeepDevice(PushDevice{User: LocalUser, Kind: KindWebPush, Name: "Mac", Target: []byte("s")}, "h0", t0)
	must(t, err)
	tm.Close()
	before0015(t, path)
	before0014(t, path)
	before0013(t, path)
	before0012(t, path)
	before0011(t, path)
	setVersion(t, path, 10)
	if tm, err = OpenTeam(path); err != nil {
		t.Fatal(err)
	}
	defer tm.Close()
	if d, ok, _ := tm.Device(old.ID); !ok || !d.Prefs.Wants("task.needs_you") || d.Prefs.Wants("task.done") || d.Prefs.Hide || d.Prefs.Wait != 0 || d.Credential != "" {
		t.Fatalf("a device from before the settings: %+v", d)
	}

	a, _ := tm.KeepDevice(PushDevice{User: "u_a", Kind: KindWebPush, Name: "Android", Target: []byte("x")}, "h1", t0)
	want := DevicePrefs{Events: []string{"task.done"}, Hide: true, Wait: 300}
	if ok, err := tm.SetDevicePrefs("u_b", a.ID, want); err != nil || ok {
		t.Fatalf("someone else's device: %v %v", ok, err)
	}
	if ok, err := tm.SetDevicePrefs("u_a", a.ID, want); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	tm.KeepDevice(PushDevice{User: "u_a", Kind: KindWebPush, Name: "Android", Target: []byte("y")}, "h1", t0.Add(time.Hour))
	ds, _ := tm.Devices("u_a")
	if len(ds) != 1 || !reflect.DeepEqual(ds[0].Prefs, want) || ds[0].Prefs.Wants("task.needs_you") || !ds[0].Prefs.Wants("task.done") {
		t.Fatalf("after renewing: %+v", ds)
	}
	if ok, err := tm.SetDevicePrefs("u_a", a.ID, DevicePrefs{Events: []string{}}); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if d, _, _ := tm.Device(a.ID); d.Prefs.Events == nil || d.Prefs.Wants("task.needs_you") || d.Prefs.Wants("task.done") {
		t.Fatalf("a device that takes nothing: %+v", d.Prefs)
	}
	if ok, err := tm.RemoveUserDevice("u_b", a.ID); err != nil || ok {
		t.Fatalf("someone else removed it: %v %v", ok, err)
	}
	if ok, err := tm.RemoveUserDevice("u_a", a.ID); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if _, ok, _ := tm.Device(a.ID); ok {
		t.Fatal("still there")
	}
}
