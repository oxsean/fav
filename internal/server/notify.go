package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/task"
)

// ⚠️ The outbox's rules: a push waits this long for the page to be enough (a permission less), each channel sends
// this many at once, a delivery is tried this many times after its first with growing waits, a device nobody renewed
// for this long goes.
const (
	pushWait       = 60 * time.Second
	pushWaitAsk    = 30 * time.Second
	sendTimeout    = 10 * time.Second
	retries        = 3
	firstRetry     = 30 * time.Second
	longestRetry   = time.Hour
	deviceLifetime = 90 * 24 * time.Hour
	pushTTL        = 24 * time.Hour
	projectURL     = "https://github.com/oxsean/fav"
)

// channelSlots is how many deliveries each channel has on the way at once.
var channelSlots = map[string]int{channelWebhook: 4, store.KindWebPush: 8}

const channelWebhook = "webhook"

// notifyCoord is what the Notifier asks the coordinator before each try: whether what a notice is about still waits
// on its recipient, what a request asks.
type notifyCoord interface {
	ID() string
	Waiting(user, task string) (coord.InboxItem, bool)
	Sees(user, task string) bool
	Request(run, id string) (agent.Request, bool)
	NoticeActs(user, task string, item task.Pending) []string
	WaitingCount(user string) int
}

// NotifyOptions: Team keeps the outbox and the devices, Seal opens the devices' targets, Push signs to push services
// (nil: no Web Push), Act signs the tokens of a push's buttons (nil: none), Base is the web page's address ("" leaves
// links out of webhooks) and names the server to push services when it is https.
type NotifyOptions struct {
	Team   *store.Team
	Coord  notifyCoord
	Seal   *Sealer
	Push   *PushKey
	Act    *ActKey
	Base   string
	Dir    *Directory // a device pushes only while its credential is live here
	Egress *Egress    // nil: public addresses only
}

// Notifier delivers the coordinator's notices through the outbox (the deliveries table): each recipient's webhook at
// once, each of their push devices once the page had its time, retried with growing waits, checked again before each
// try, and carried on after a restart.
type Notifier struct {
	mu     sync.Mutex
	o      *NotifyOptions
	early  []coord.Notice // the ones before Start
	flying map[string]bool
	slots  map[string]chan struct{}
	wake   chan struct{}
	wg     sync.WaitGroup
	now    func() time.Time
	client *http.Client
}

func NewNotifier() *Notifier {
	n := &Notifier{flying: map[string]bool{}, slots: map[string]chan struct{}{}, wake: make(chan struct{}, 1), now: time.Now}
	for ch, k := range channelSlots {
		n.slots[ch] = make(chan struct{}, k)
	}
	return n
}

// Send keeps x in the outbox, one row per recipient's webhook and push device; it runs under the coordinator's lock,
// a local write like the envelope's own. Before Start it holds x until the outbox is there.
func (n *Notifier) Send(x coord.Notice) {
	n.mu.Lock()
	o := n.o
	if o == nil {
		n.early = append(n.early, x)
	}
	n.mu.Unlock()
	if o != nil {
		n.keep(o, x)
	}
}

func (n *Notifier) keep(o *NotifyOptions, x coord.Notice) {
	body, _ := json.Marshal(noticeRow{x.Seq, x.Event, x.Task, x.Title, x.Project, x.Reason, x.Run, x.Stage, x.Items, x.At})
	var rows []store.Delivery
	for _, u := range x.To {
		row := store.Delivery{Seq: x.Seq, User: u, Event: x.Event, Next: x.At, Notice: body, At: x.At}
		if hook, _ := o.Team.Webhook(u); hook != "" {
			rows = append(rows, row)
		}
		if o.Push == nil || x.Event != coord.NotifyTaskWaiting && x.Event != coord.NotifyTaskDone {
			continue
		}
		devices, err := o.Team.Devices(u)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tend-server: push devices:", err)
		}
		for _, d := range devices {
			if !d.Prefs.Wants(x.Event) {
				continue
			}
			row.Device, row.Next = d.ID, x.At
			if x.Event == coord.NotifyTaskWaiting {
				row.Next = x.At.Add(waitFor(d.Prefs, x.Items))
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return
	}
	if _, err := o.Team.Enqueue(rows); err != nil {
		fmt.Fprintln(os.Stderr, "tend-server: notice not kept:", x.Task, x.Event, err)
	}
	n.poke()
}

// noticeRow is a notice as the outbox keeps it: without its recipients, each row is one of them.
type noticeRow struct {
	Seq     int64          `json:"seq"`
	Event   string         `json:"event"`
	Task    string         `json:"task,omitempty"`
	Title   string         `json:"title,omitempty"`
	Project string         `json:"project,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Run     string         `json:"run,omitempty"`
	Stage   string         `json:"stage,omitempty"`
	Items   []task.Pending `json:"items,omitempty"`
	At      time.Time      `json:"at"`
}

// waitFor is how long a push waits for the page to be enough: as long as the device asks, else less when an agent asks
// to use a tool.
func waitFor(prefs store.DevicePrefs, items []task.Pending) time.Duration {
	switch {
	case prefs.Wait < 0:
		return 0
	case prefs.Wait > 0:
		return time.Duration(prefs.Wait) * time.Second
	}
	for _, p := range items {
		if p.Kind == task.PendPermission {
			return pushWaitAsk
		}
	}
	return pushWait
}

func (n *Notifier) poke() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// Start delivers until ctx ends: what the outbox holds from before a restart first, then each notice as it comes.
func (n *Notifier) Start(ctx context.Context, o NotifyOptions) {
	n.attach(o)
	go n.loop(ctx)
}

func (n *Notifier) attach(o NotifyOptions) {
	n.mu.Lock()
	n.o = &o
	if n.client == nil {
		e := o.Egress
		if e == nil {
			e = &Egress{}
		}
		n.client = e.Client(sendTimeout)
	}
	early := n.early
	n.early = nil
	n.mu.Unlock()
	for _, x := range early {
		n.keep(&o, x)
	}
}

func (n *Notifier) loop(ctx context.Context) {
	var swept time.Time
	for {
		now := n.now()
		if now.Sub(swept) >= time.Hour {
			if _, err := n.o.Team.ExpireDevices(now.Add(-deviceLifetime)); err != nil {
				fmt.Fprintln(os.Stderr, "tend-server: push devices:", err)
			}
			swept = now
		}
		n.dispatch(ctx)
		wait := time.Minute
		if next, ok, _ := n.o.Team.NextDue(); ok && next.After(now) && next.Sub(now) < wait {
			wait = next.Sub(now)
		} // one due already is on its way or waits for a slot: a delivery that ends pokes
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			n.wg.Wait()
			return
		case <-n.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// dispatch starts the deliveries that are due, each on its channel's slots, one at a time per device, and answers
// how many it started.
func (n *Notifier) dispatch(ctx context.Context) int {
	o := n.o
	due, err := o.Team.Due(n.now(), 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tend-server: outbox:", err)
		return 0
	}
	started := 0
	for _, d := range due {
		ch, key := channelWebhook, "hook:"+d.User
		var dev store.PushDevice
		if d.Device != "" {
			var ok bool
			if dev, ok, err = o.Team.Device(d.Device); err != nil {
				continue
			} else if !ok {
				o.Team.Settle(d.ID, store.DeliveryGone, "no device", d.Attempts, time.Time{})
				continue
			}
			ch, key = dev.Kind, d.Device
		}
		slots := n.slots[ch]
		if slots == nil {
			o.Team.Settle(d.ID, store.DeliveryFailed, "no channel "+ch, d.Attempts, time.Time{})
			continue
		}
		n.mu.Lock()
		busy := n.flying[key]
		n.mu.Unlock()
		if busy {
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			continue
		}
		n.mu.Lock()
		n.flying[key] = true
		n.mu.Unlock()
		n.wg.Add(1)
		started++
		go func() {
			defer func() {
				n.mu.Lock()
				delete(n.flying, key)
				n.mu.Unlock()
				<-slots
				n.wg.Done()
				n.poke()
			}()
			n.deliver(ctx, d, dev)
		}()
	}
	return started
}

// deliver tries d once, if what it is about still is, and records how it went.
func (n *Notifier) deliver(ctx context.Context, d store.Delivery, dev store.PushDevice) {
	o := n.o
	if d.Device != "" && dev.User != d.User {
		o.Team.Settle(d.ID, store.DeliveryCanceled, "another owner", d.Attempts, time.Time{})
		return
	}
	if d.Device != "" && o.Dir != nil && !o.Dir.live(dev.Credential) {
		o.Team.Settle(d.ID, store.DeliveryCanceled, "signed out", d.Attempts, time.Time{})
		return
	}
	var x noticeRow
	if err := json.Unmarshal(d.Notice, &x); err != nil {
		o.Team.Settle(d.ID, store.DeliveryFailed, "bad notice", d.Attempts, time.Time{})
		return
	}
	item, ok := n.wanted(d.User, x)
	if !ok {
		o.Team.Settle(d.ID, store.DeliveryCanceled, "", d.Attempts, time.Time{})
		return
	}
	var gone bool
	var err error
	if d.Device == "" {
		hook, herr := o.Team.Webhook(d.User)
		if herr == nil && hook == "" {
			o.Team.Settle(d.ID, store.DeliveryCanceled, "no webhook", d.Attempts, time.Time{})
			return
		}
		err = errors.Join(herr, n.webhook(ctx, hook, x))
	} else {
		gone, err = n.webpush(ctx, d.User, dev, x, item)
	}
	n.settle(d, dev, gone, err)
}

// wanted: x still is for user: they are there and, for something waiting, it still waits on them (one of the items
// it came with, at its version), else they may still see its task. For something waiting it answers what waits now.
func (n *Notifier) wanted(user string, x noticeRow) (coord.InboxItem, bool) {
	if u, ok, err := n.o.Team.User(user); err != nil || !ok || u.Disabled {
		return coord.InboxItem{}, false
	}
	switch {
	case x.Event == coord.NotifyTaskWaiting:
		it, ok := n.o.Coord.Waiting(user, x.Task)
		for _, p := range x.Items {
			if ok && slices.ContainsFunc(it.Pending, func(q task.Pending) bool { return q.ID == p.ID && q.Version == p.Version }) {
				return it, true
			}
		}
		return coord.InboxItem{}, false
	case x.Task != "":
		return coord.InboxItem{}, n.o.Coord.Sees(user, x.Task)
	}
	return coord.InboxItem{}, true
}

// settle records a try: done, the device gone, tried again after a growing wait (as long as the service asks, at
// least), or failed for good.
func (n *Notifier) settle(d store.Delivery, dev store.PushDevice, gone bool, err error) {
	team := n.o.Team
	tries := d.Attempts + 1
	result := resultOf(err)
	switch {
	case gone:
		team.RemoveDevice(dev.ID, result)
	case err == nil:
		team.Settle(d.ID, store.DeliveryOK, result, tries, time.Time{})
		if dev.ID != "" {
			team.DeviceWorked(dev.ID, n.now())
		}
	default:
		if dev.ID != "" {
			team.DeviceFailed(dev.ID)
		}
		var se *sendError
		final := errors.As(err, &se) && se.final() || errors.Is(err, errEgress)
		if final || tries > retries {
			team.Settle(d.ID, store.DeliveryFailed, result, tries, time.Time{})
			return
		}
		wait := firstRetry << (2 * (tries - 1))
		if se != nil && se.retry > wait {
			wait = se.retry
		}
		team.Settle(d.ID, store.DeliveryPending, result, tries, n.now().Add(min(wait, longestRetry)))
	}
}

// sendError is a channel's answer that is not a success: the HTTP status and how long the service asked to wait.
type sendError struct {
	status int
	retry  time.Duration
}

func (e *sendError) Error() string { return strconv.Itoa(e.status) }

// final: trying again gets the same answer; a redirect not followed is one.
func (e *sendError) final() bool {
	return e.status >= 300 && e.status < 500 && e.status != http.StatusRequestTimeout && e.status != http.StatusTooManyRequests
}

func resultOf(err error) string {
	if err == nil {
		return "ok"
	}
	var se *sendError
	if errors.As(err, &se) {
		return se.Error()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return ue.Op + ": timeout"
		}
		err = ue.Err // ue names the endpoint, whose path is the device's secret
	}
	s := err.Error()
	var oe *net.OpError
	if errors.As(err, &oe) {
		s = oe.Op + ": " + oe.Err.Error()
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// answer turns an HTTP answer into a channel's: nil for a success, gone for 404 and 410 when the channel says a
// device went (Web Push), else a sendError.
func answer(res *http.Response, deviceGoes bool) (gone bool, err error) {
	res.Body.Close()
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return false, nil
	case deviceGoes && (res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone):
		return true, &sendError{status: res.StatusCode}
	}
	se := &sendError{status: res.StatusCode}
	if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
		se.retry = time.Duration(s) * time.Second
	} else if t, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
		se.retry = t.Sub(time.Now())
	}
	return false, se
}

// WebhookPayload is what a user's webhook receives.
type WebhookPayload struct {
	Event   string    `json:"event"`
	Task    string    `json:"task"`
	Title   string    `json:"title"`
	Project string    `json:"project,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Run     string    `json:"run,omitempty"`
	URL     string    `json:"url,omitempty"` // the task on the web page
	At      time.Time `json:"at"`
	Text    string    `json:"text"` // one line, for chat webhooks
}

// webhook posts x to hook.
func (n *Notifier) webhook(ctx context.Context, hook string, x noticeRow) error {
	p := WebhookPayload{Event: x.Event, Task: x.Task, Title: x.Title, Project: x.Project, Reason: x.Reason, Run: x.Run, At: x.At,
		Text: x.Title + " · " + strings.TrimPrefix(x.Event, "task.") + " " + x.Reason}
	if n.o.Base != "" && x.Task != "" {
		p.URL = strings.TrimRight(n.o.Base, "/") + "/#task-" + x.Task
	}
	return postWebhook(ctx, n.client, hook, p)
}

// postWebhook posts p to hook: nil for a 2xx answer, a *sendError with the status for another.
func postWebhook(ctx context.Context, client *http.Client, hook string, p WebhookPayload) error {
	b, _ := json.Marshal(p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(b))
	if err != nil {
		return &sendError{status: http.StatusBadRequest}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tend-server")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	_, err = answer(res, false)
	return err
}

// PushMessage is what a push to a browser carries (encrypted end to end): what waits on its recipient now about a
// task, for the service worker to show.
type PushMessage struct {
	V       int          `json:"v"`
	Server  string       `json:"server"`
	Seq     int64        `json:"seq"`
	Event   string       `json:"event"`
	Task    string       `json:"task,omitempty"`
	Item    string       `json:"item,omitempty"`
	Kind    string       `json:"kind,omitempty"`
	Title   string       `json:"title,omitempty"`
	What    string       `json:"what,omitempty"` // what a permission would do: its command or file
	Project string       `json:"project,omitempty"`
	N       int          `json:"n"` // how many things of the task wait on them; with the content hidden, how many wait at all
	Link    string       `json:"link,omitempty"`
	At      time.Time    `json:"at,omitzero"`
	Actions []PushAction `json:"actions,omitempty"` // the buttons that act, besides the one that opens the page
}

// PushAction is a button of a push: the service worker sends its token to /api/act.
type PushAction struct {
	Action string `json:"action"`
	Token  string `json:"token"`
}

// ⚠️ The service worker's name for the button that denies a permission.
const pushReject = "reject"

// message is what a push about x says to user on device dev now that item is how the task waits on them: a
// permission they may deny gets a button for it.
func (n *Notifier) message(user string, dev store.PushDevice, x noticeRow, item coord.InboxItem) PushMessage {
	if dev.Prefs.Hide {
		m := PushMessage{V: 1, Server: n.o.Coord.ID(), Seq: x.Seq, Event: x.Event}
		if x.Event == coord.NotifyTaskWaiting {
			m.N = n.o.Coord.WaitingCount(user)
		}
		return m
	}
	if x.Event == coord.NotifyTaskDone {
		return PushMessage{V: 1, Server: n.o.Coord.ID(), Seq: x.Seq, Event: x.Event, Task: x.Task, Title: clipRunes(x.Title, 200), Project: x.Project,
			Link: "#task-" + x.Task, At: x.At}
	}
	m := PushMessage{V: 1, Server: n.o.Coord.ID(), Seq: x.Seq, Event: x.Event, Task: x.Task, Title: clipRunes(item.Title, 200),
		Project: item.Project, N: len(item.Pending), Link: "#wait-" + x.Task, At: x.At}
	for _, p := range x.Items {
		i := slices.IndexFunc(item.Pending, func(q task.Pending) bool { return q.ID == p.ID && q.Version == p.Version })
		if i < 0 {
			continue
		}
		p = item.Pending[i]
		m.Item, m.Kind = p.ID, p.Kind
		if p.Kind == task.PendPermission && p.Request != "" {
			if r, ok := n.o.Coord.Request(p.Run, p.Request); ok {
				m.What = clipRunes(cmp.Or(r.Summary, r.Tool), 300)
			}
		}
		if n.o.Act != nil && slices.Contains(n.o.Coord.NoticeActs(user, x.Task, p), coord.ActDeny) {
			tok := n.o.Act.mint(actClaim{User: user, Task: x.Task, Item: p.ID, Version: p.Version, Action: coord.ActDeny, Seq: x.Seq, Device: dev.ID,
				Until: n.now().Add(actLife).Unix()})
			m.Actions = []PushAction{{Action: pushReject, Token: tok}}
		}
		break
	}
	return m
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// subject names this server to push services (RFC 8292 sub): its address when that is https, else the project's.
func (o *NotifyOptions) subject() string {
	if strings.HasPrefix(o.Base, "https://") {
		return strings.TrimRight(o.Base, "/")
	}
	return projectURL
}

// webpush sends what waits (item) to the browser dev, encrypted for it; a push service that no longer knows the
// subscription (404, 410) says the device is gone.
func (n *Notifier) webpush(ctx context.Context, user string, dev store.PushDevice, x noticeRow, item coord.InboxItem) (bool, error) {
	plain, err := n.o.Seal.Open(dev.Target)
	var sub webSubscription
	if err == nil {
		err = json.Unmarshal(plain, &sub)
	}
	if err != nil {
		return false, &sendError{status: http.StatusBadRequest}
	}
	m := n.message(user, dev, x, item)
	b, _ := json.Marshal(m)
	body, err := sealPush(sub, b)
	if err != nil {
		return false, &sendError{status: http.StatusRequestEntityTooLarge}
	}
	auth, err := n.o.Push.vapid(sub.Endpoint, n.o.subject(), n.now())
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, &sendError{status: http.StatusBadRequest}
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(pushTTL/time.Second)))
	urgency := "normal"
	if m.Kind == task.PendPermission || m.Kind == task.PendQuestion {
		urgency = "high"
	}
	req.Header.Set("Urgency", urgency)
	switch {
	case m.Task == "" && m.Event == coord.NotifyTaskDone:
		req.Header.Set("Topic", "tend-done")
	case m.Task == "":
		req.Header.Set("Topic", "tend") // one count takes the last one's place
	case topic.MatchString(m.Task):
		req.Header.Set("Topic", m.Task)
	}
	res, err := n.client.Do(req)
	if err != nil {
		return false, err
	}
	return answer(res, true)
}

// ⚠️ RFC 8030 5.4: a topic is at most 32 characters of the URL- and filename-safe base64 alphabet.
var topic = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// CheckWebhook: u is a webhook a user may set: http or https, at most 1024 bytes.
func CheckWebhook(u string) error {
	if u == "" {
		return nil
	}
	x, err := url.Parse(u)
	if err != nil || len(u) > 1024 || x.Scheme != "http" && x.Scheme != "https" || x.Host == "" {
		return fmt.Errorf("webhook %q: an http or https address", u)
	}
	return nil
}
