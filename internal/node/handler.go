package node

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

// Methods a node answers besides the session reads.
const (
	MRunStart     = "run.start"
	MRunStop      = "run.stop"
	MRunList      = "run.list"
	MRunTail      = "run.tail"
	MRunLine      = "run.line"      // one whole line of a run's output (LineParams)
	MRunResume    = "run.resume"    // run.start continuing a session (StartParams.Resume)
	MRunAnswer    = "run.answer"    // an answer to what a stream run waits on (AnswerParams)
	MRunSend      = "run.send"      // a message for a running stream run (SendParams)
	MRunInterrupt = "run.interrupt" // end a turn of a running stream run (InterruptParams)
	MAgents       = "node.agents"   // how each agent CLI stands here
	MChanged      = "node.changed"  // push: a run's state changed
)

type RunRef struct {
	Run         string `json:"run"`
	Coordinator string `json:"coordinator,omitempty"` // run.stop: whose run a stop before its start leaves behind
}

type ListParams struct {
	Coordinator string   `json:"coordinator"`
	Ack         []string `json:"ack,omitempty"`  // finished runs the coordinator has recorded
	Runs        []string `json:"runs,omitempty"` // only these runs ("" all); an older node lists all
}

type Runs struct {
	Runs []Snapshot `json:"runs"`
}

// Handler answers the node methods and, through sessions, the session reads.
func (n *Node) Handler(sessions remote.Handler) wire.Handler {
	return func(ctx context.Context, r *wire.Request) (any, error) {
		switch r.Method {
		case MRunStart, MRunResume:
			var p StartParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			if r.Method == MRunStart {
				p.Resume = ""
			} else if p.Resume == "" {
				return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "resume"}
			}
			return n.Start(p)
		case MAgents:
			var p ChecksParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Checks(p.Fresh), nil
		case MRunStop:
			var p RunRef
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Stop(p)
		case MRunList:
			var p ListParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			runs, err := n.List(p.Coordinator, p.Ack, p.Runs...)
			return Runs{Runs: runs}, err
		case MRunTail:
			var p TailParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Tail(p)
		case MRunFollow:
			var p FollowParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return nil, n.Follow(r, p)
		case MRunLine:
			var p LineParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Line(p)
		case MRunAnswer:
			var p AnswerParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Answer(p)
		case MRunSend:
			var p SendParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Send(p)
		case MRunInterrupt:
			var p InterruptParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Interrupt(p)
		case MDirs:
			var p DirsParams
			if err := r.Decode(&p); err != nil {
				return nil, err
			}
			return n.Dirs(p)
		}
		res, err := shareSessions(ctx, n.Limits.ShareSessions, sessions, r.Method, r.Params)
		if h, ok := res.(remote.Hello); ok && err == nil {
			h.Methods = append(append([]string(nil), h.Methods...), Methods...)
			h.Features = append(append([]string(nil), h.Features...), Features...)
			h.NodeID = n.ID()
			return h, nil
		}
		return res, err
	}
}

// Methods lists what Handler answers.
var Methods = []string{MRunStart, MRunStop, MRunList, MRunTail, MRunLine, MRunResume, MAgents, MRunAnswer, MRunSend, MRunInterrupt, MDirs, MRunFollow}

// Features lists what run.start and run.resume understand beyond their first shape; a coordinator that needs a feature
// this node lacks fails the run as node_outdated instead of starting it without.
var Features = []string{FeatureDispatcher, FeatureAgentDef, FeatureVerdict, FeatureCheck, FeatureWorktree, FeatureFiles, FeaturePlan, FeatureBeforeRun,
	FeatureInputMarks, FeatureInterrupt, FeatureAnswerScope}

// watchEvery is how often Watch looks at the runs.
const watchEvery = 3 * time.Second

// Watch calls changed with the runs whose state moved, until done closes.
func (n *Node) Watch(done <-chan struct{}, changed func(runs []string)) {
	var revs map[string]int
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		ents, _ := os.ReadDir(filepath.Join(n.Dir, "runs"))
		var moved []string
		first := revs == nil
		if first {
			revs = map[string]int{}
		}
		for _, e := range ents {
			s, err := n.Snapshot(e.Name())
			if err != nil {
				continue
			}
			key := s.Rev
			if s.State.State == StateUnknown || s.Reason == "not_launched" {
				key = -1
			}
			if old, ok := revs[e.Name()]; !first && (!ok || old != key) {
				moved = append(moved, e.Name())
			}
			revs[e.Name()] = key
		}
		if len(moved) > 0 {
			changed(moved)
		}
	}
}

// Changed is a node.changed push.
type Changed struct {
	Runs []string `json:"runs"`
}
