package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
)

// Notifier posts the coordinator's notices to each recipient's own webhook, once per user.
type Notifier struct {
	q      chan coord.Notice
	client *http.Client
}

func NewNotifier() *Notifier {
	return &Notifier{q: make(chan coord.Notice, 256), client: &http.Client{Timeout: 10 * time.Second}}
}

// Send queues n; it never blocks the coordinator, and drops n when the queue is full.
func (n *Notifier) Send(x coord.Notice) {
	select {
	case n.q <- x:
	default:
		fmt.Fprintln(os.Stderr, "tend-server: notice dropped:", x.Task, x.Event)
	}
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

// Run delivers until ctx ends; base is the web page's address ("" leaves the link out).
func (n *Notifier) Run(ctx context.Context, team *store.Team, base string) {
	for {
		select {
		case <-ctx.Done():
			return
		case x := <-n.q:
			for _, u := range x.To {
				n.deliver(ctx, team, base, x, u)
			}
		}
	}
}

func (n *Notifier) deliver(ctx context.Context, team *store.Team, base string, x coord.Notice, user string) {
	hook, err := team.Webhook(user)
	if err != nil || hook == "" {
		return
	}
	if x.Seq > 0 { // a notice of the journal goes once; the server's own (a tracker that stopped) each time
		if first, err := team.Claim(x.Seq, user, x.Event); err != nil || !first {
			return
		}
	}
	p := WebhookPayload{Event: x.Event, Task: x.Task, Title: x.Title, Project: x.Project, Reason: x.Reason, Run: x.Run, At: x.At,
		Text: x.Title + " · " + strings.TrimPrefix(x.Event, "task.") + " " + x.Reason}
	if base != "" && x.Task != "" {
		p.URL = strings.TrimRight(base, "/") + "/#task-" + x.Task
	}
	b, _ := json.Marshal(p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(b))
	status := "error"
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "tend-server")
		if res, err := n.client.Do(req); err == nil {
			res.Body.Close()
			status = strconv.Itoa(res.StatusCode)
		}
	}
	if x.Seq > 0 {
		team.Delivered(x.Seq, user, x.Event, status)
	}
}

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
