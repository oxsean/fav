package coord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// ActDeny is a notice's button that declines what a permission asks.
const ActDeny = "deny"

// ActOn is an action taken from a notice: on its item at the version the notice named.
type ActOn struct {
	Task    string `json:"task"`
	Item    string `json:"item"`
	Version int64  `json:"version"`
	Action  string `json:"action"`
}

// NoticeActs are the actions user may take from a notice about item of task id now.
func (c *Coord) NoticeActs(user, id string, item task.Pending) []string {
	if item.Kind != task.PendPermission || item.Run == "" || item.Request == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.principal(user)
	if !ok || c.stillPending(p, ActOn{Task: id, Item: item.ID, Version: item.Version}) != nil ||
		!dry(p, c.runAnswer, Answer{Run: item.Run, Answer: agent.Answer{Request: item.Request, Decision: agent.DecisionDeny}}) {
		return nil
	}
	return []string{ActDeny}
}

// Act does on's action for user while its item still waits at its version, the method and its params made from the
// state, not from the notice. The same action on the same item at the same version is one command, however many
// devices send it.
func (c *Coord) Act(user string, on ActOn) error {
	run, req, ok := strings.Cut(on.Item, "/")
	if on.Action != ActDeny {
		return bad("action")
	}
	c.mu.Lock()
	p, known := c.principal(user)
	asks := ok && c.st.Runs[run] != nil
	c.mu.Unlock()
	switch {
	case !known:
		return forbidden("user")
	case !asks:
		return bad("item") // only a run's request is denied
	}
	if err := p.may(MRunAnswer); err != nil {
		return err
	}
	params, _ := json.Marshal(Answer{Run: run, Answer: agent.Answer{Request: req, Decision: agent.DecisionDeny}})
	sum := sha256.Sum256([]byte(strings.Join([]string{user, on.Task, on.Item, strconv.FormatInt(on.Version, 10), on.Action}, "\x00")))
	r := &wire.Request{Method: MRunAnswer, CommandID: "act-" + hex.EncodeToString(sum[:12]), Params: params}
	_, err := c.command(p, r, func(p Principal, r *wire.Request) (string, []journal.Event, error) {
		if err := c.stillPending(p, on); err != nil {
			return "", nil, err
		}
		return c.runAnswer(p, r)
	}, c.runViewFor(p))
	return err
}

// stillPending: on's item waits in its task at its version, as p may see it; else who answered it. The caller holds mu.
func (c *Coord) stillPending(p Principal, on ActOn) error {
	t := c.st.Tasks[on.Task]
	if !c.canRead(c.st, p, t) {
		return notFound(on.Task)
	}
	if slices.ContainsFunc(c.st.Pending(t), func(q task.Pending) bool { return q.ID == on.Item && q.Version == on.Version }) {
		return nil
	}
	run, req, _ := strings.Cut(on.Item, "/")
	if r := c.st.Runs[run]; r != nil && r.Task == t.ID {
		if i := slices.IndexFunc(r.Answers, func(a agent.Answer) bool { return a.Request == req }); i >= 0 {
			return requestGone(r.Answers[i].By)
		}
	}
	return requestGone("")
}
