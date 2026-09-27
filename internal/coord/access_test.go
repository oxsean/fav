package coord

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

func TestEveryClientMethodHasAnAccessRule(t *testing.T) {
	for _, m := range append([]string{wire.MPing, remote.MHello}, Methods...) {
		if _, ok := methodAccess[m]; !ok {
			t.Errorf("%s has no access rule: it would be refused", m)
		}
	}
	for m := range methodAccess {
		if m != wire.MPing && m != remote.MHello && !slices.Contains(Methods, m) {
			t.Errorf("%s has a rule but is not a client method", m)
		}
	}
}

// as is a client of e's coordinator calling as p.
func (e *env) as(p Principal) *wire.Conn {
	cli, _ := wire.Pipe(wire.Options{}, wire.Options{Handler: e.c.HandlerFor(p)})
	e.t.Cleanup(func() { cli.Close() })
	return cli
}

func callAs(cli *wire.Conn, method, commandID string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if commandID == "" {
		return cli.Call(ctx, method, params, out)
	}
	return cli.CallCommand(ctx, method, commandID, params, out)
}

func TestCallersWithoutTheRightAreRefused(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	nobody, member := e.as(Principal{}), e.as(Principal{User: "ann"})
	var h remote.Hello
	if err := callAs(nobody, remote.MHello, "", remote.HelloParams{Proto: wire.Proto}, &h); err != nil {
		t.Fatalf("hello is for anyone: %v", err)
	}
	if err := callAs(nobody, MStateGet, "", StateParams{}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("no user reads nothing: %v", err)
	}
	if err := callAs(member, MNodeCall, "", NodeCall{Machine: Local, Method: remote.MList}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("node.call reaches a machine's own sessions: admins only: %v", err)
	}
	cont := Continue{Session: "s1", Provider: tend.ProviderClaude, Dir: t.TempDir(), Text: "go on"}
	if err := callAs(member, MRunContinue, "c1", cont, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("continuing any session is for admins: %v", err)
	}
	var tk task.Task
	if err := callAs(member, MTaskCreate, "c2", TaskCreate{Title: "a task", Dir: t.TempDir()}, &tk); err != nil {
		t.Fatalf("a member writes tasks: %v", err)
	}
	if err := callAs(member, "no.such", "", nil, nil); wire.Code(err) != wire.CodeUnknownMethod {
		t.Fatalf("an unknown method stays unknown: %v", err)
	}
}

func TestCommandIDsBelongToTheirCaller(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	ann, bob := e.as(Principal{User: "ann", Admin: true}), e.as(Principal{User: "bob", Admin: true})
	p := TaskCreate{Title: "same id", Dir: t.TempDir()}
	var a1, a2, b1 task.Task
	if err := callAs(ann, MTaskCreate, "shared", p, &a1); err != nil {
		t.Fatal(err)
	}
	if err := callAs(ann, MTaskCreate, "shared", p, &a2); err != nil || a2.ID != a1.ID {
		t.Fatalf("ann's replay answers her first call: %v %s %s", err, a1.ID, a2.ID)
	}
	if err := callAs(bob, MTaskCreate, "shared", p, &b1); err != nil || b1.ID == a1.ID {
		t.Fatalf("bob's command with ann's id is his own: %v %s %s", err, a1.ID, b1.ID)
	}

	var got []journal.Envelope
	e.c.log.ReadAfter(0, e.c.log.Seq(), func(env journal.Envelope) bool { got = append(got, env); return true })
	if len(got) != 2 || got[0].Who() != (journal.Actor{Kind: journal.ActorUser, ID: "ann"}) || got[1].Who().ID != "bob" {
		b, _ := json.Marshal(got)
		t.Fatalf("each envelope names its caller: %s", b)
	}

	e.stop()
	e.start()
	ann = e.as(Principal{User: "ann", Admin: true})
	var again task.Task
	if err := callAs(ann, MTaskCreate, "shared", p, &again); err != nil || again.ID != a1.ID {
		t.Fatalf("receipts come back per caller from the journal: %v %s %s", err, a1.ID, again.ID)
	}
}

func TestPushesLeaveACommandsResultWithItsCaller(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	got := make(chan journal.Envelope, 1)
	cli, _ := wire.Pipe(wire.Options{OnPush: func(_ string, params json.RawMessage) {
		var env journal.Envelope
		json.Unmarshal(params, &env)
		got <- env
	}}, wire.Options{Handler: e.c.HandlerFor(Principal{User: "bob", Admin: true})})
	defer cli.Close()
	if err := callAs(cli, MSubscribe, "", SubscribeParams{}, nil); err != nil {
		t.Fatal(err)
	}
	e.task("ann's task", "quick")
	select {
	case env := <-got:
		if c := env.Command; c == nil || c.ID == "" || c.Method != MTaskCreate || c.Result != nil || c.Digest != "" {
			t.Fatalf("a push names the command, nothing more: %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no push")
	}
}
