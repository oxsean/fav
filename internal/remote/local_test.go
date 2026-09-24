package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
)

func localMachine(t *testing.T) (*fixture.Dataset, *Client) {
	t.Helper()
	d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAV_HOME", d.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
	t.Setenv("CODEX_HOME", d.Codex)
	return d, pipeClient(t, NewLocal("test"))
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestLocalHello(t *testing.T) {
	_, c := localMachine(t)
	defer i18n.Set(i18n.ZH)
	var h Hello
	if err := c.Call(context.Background(), MHello, HelloParams{Lang: i18n.EN}, &h); err != nil {
		t.Fatal(err)
	}
	if h.Proto != Proto || h.Version != "test" || h.OS == "" || h.Arch == "" || len(h.Endpoint) != 12 || h.Home == "" || h.Sep == "" {
		t.Fatalf("%+v", h)
	}
	if _, ok := h.CLIs[fav.ProviderCodex]; !ok || len(h.CLIs) != 2 {
		t.Errorf("clis: %v", h.CLIs)
	}
	for _, m := range []string{MHello, MList, MMessages, MText, MSteps, MPulse, MChecks, MLive, MEcho} {
		if !slices.Contains(h.Methods, m) {
			t.Errorf("methods lack %s: %v", m, h.Methods)
		}
	}
	if i18n.T("remote.err.offline") != "offline" {
		t.Error("hello's lang sets the language check texts come back in")
	}
	var again Hello
	c.Call(context.Background(), MHello, nil, &again)
	t.Setenv("CODEX_HOME", t.TempDir())
	var other Hello
	c.Call(context.Background(), MHello, nil, &other)
	if again.Endpoint != h.Endpoint || other.Endpoint == h.Endpoint {
		t.Errorf("the endpoint is stable and follows the config dirs: %s %s %s", h.Endpoint, again.Endpoint, other.Endpoint)
	}
}

func TestLocalListsEverySessionAndFollowsTheStore(t *testing.T) {
	d, c := localMachine(t)
	list := func() map[string]Session {
		t.Helper()
		var l List
		if err := c.Call(context.Background(), MList, nil, &l); err != nil {
			t.Fatal(err)
		}
		out := map[string]Session{}
		for _, s := range l.Sessions {
			out[fav.SessionKey(s.Provider, s.SessionID)] = s
		}
		return out
	}
	got := list()
	for _, s := range d.Sessions {
		_, ok := got[fav.SessionKey(s.Provider, s.ID)]
		if want := !s.Agent && s.Name != "chain-old"; ok != want {
			t.Errorf("%s: listed %v, want %v (short sessions included, agent runs not)", s.Name, ok, want)
		}
	}
	oauth := d.Get("oauth")
	if s := got[fav.SessionKey(oauth.Provider, oauth.ID)]; s.Turns != 4 || s.Transcript == "" || s.LastAt.IsZero() {
		t.Errorf("index fields travel with the list: %+v", s)
	}
	arch := d.Get("archived")
	if s := got[fav.SessionKey(arch.Provider, arch.ID)]; s.ID == "" || s.ArchivedAt == nil || s.FavoritedAt == nil {
		t.Errorf("archived favorites are listed with their record: %+v", s)
	}

	store, err := fav.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(store.BySession(arch.Provider, arch.ID), func(r *fav.Rec) { r.Title = "改过的标题" }); err != nil {
		t.Fatal(err)
	}
	if s := list()[fav.SessionKey(arch.Provider, arch.ID)]; s.Title != "改过的标题" {
		t.Errorf("the next list reloads the store: %q", s.Title)
	}
}

func TestLocalReadsByRef(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	ref := func(name string) Ref { s := d.Get(name); return Ref{s.Provider, s.ID} }

	var p capture.Page
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref("oauth"), Before: -1, N: 2}, &p); err != nil || len(p.Msgs) != 2 {
		t.Fatalf("messages: %v %+v", err, p)
	}
	var tx Text
	if err := c.Call(ctx, MText, TextParams{Ref: ref("oauth"), Off: p.Msgs[0].Off, Fallback: "x"}, &tx); err != nil || tx.Text == "" || tx.Text == "x" {
		t.Errorf("text: %v %q", err, tx.Text)
	}
	var pr PulseResult
	if err := c.Call(ctx, MPulse, ref("oauth"), &pr); err != nil || !pr.OK || pr.Pulse.Size == 0 {
		t.Errorf("pulse: %v %+v", err, pr)
	}
	var ch Checks
	if err := c.Call(ctx, MChecks, ref("missing-dir"), &ch); err != nil || len(ch.Checks) == 0 {
		t.Errorf("checks: %v %+v", err, ch)
	}
	var st Steps
	if err := c.Call(ctx, MSteps, StepsParams{Ref: ref("oauth"), Steps: []capture.Step{{Text: "kept"}}}, &st); err != nil || len(st.Texts) != 1 {
		t.Errorf("steps: %v %+v", err, st)
	}
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref("sdk"), Before: -1, N: 5}, &p); err != nil || len(p.Msgs) == 0 {
		t.Errorf("an agent run the list leaves out still reads: %v %+v", err, p)
	}
	var l Live
	if err := c.Call(ctx, MLive, nil, &l); err != nil {
		t.Errorf("live: %v", err)
	}

	for _, e := range []struct {
		name   string
		method string
		params any
		want   string
	}{
		{"unknown session", MMessages, MessagesParams{Ref: Ref{fav.ProviderClaude, "nope"}, Before: -1, N: 5}, CodeNotFound},
		{"no ref", MPulse, Ref{}, CodeBadRequest},
		{"no page size", MMessages, MessagesParams{Ref: ref("oauth"), Before: -1, N: 0}, CodeBadRequest},
		{"params of the wrong shape", MChecks, json.RawMessage(`[1]`), CodeBadRequest},
		{"unknown method", "grep", nil, CodeUnknownMethod},
	} {
		if err := c.Call(ctx, e.method, e.params, nil); code(err) != e.want {
			t.Errorf("%s: %v, want %s", e.name, err, e.want)
		}
	}
	const odd = "中文 ✓ \"q\" 'x' %PATH% $HOME"
	if err := c.Call(ctx, MEcho, Text{odd}, &tx); err != nil || tx.Text != odd {
		t.Errorf("echo: %v %q", err, tx.Text)
	}
	if c.Err() != nil || strings.Contains(tx.Text, "\x00") {
		t.Error("errors from the handler leave the client working")
	}
}

func TestARewrittenTranscriptIsStale(t *testing.T) {
	d, c := localMachine(t)
	ctx := context.Background()
	s := d.Get("oauth")
	ref := Ref{s.Provider, s.ID}
	var p capture.Page
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: -1, N: 2}, &p); err != nil || p.File == "" {
		t.Fatalf("a page names its file: %v %q", err, p.File)
	}
	old := p.File
	b, _ := os.ReadFile(s.Path)
	if err := fileio.WriteAtomic(s.Path, 0o644, func(w io.Writer) error { _, err := w.Write(b); return err }); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, MMessages, MessagesParams{Ref: ref, Before: p.From, N: 2, File: old}, nil); code(err) != CodeStale {
		t.Fatalf("an older page of the old file: %v", err)
	}
	if err := c.Call(ctx, MText, TextParams{Ref: ref, Off: p.Msgs[0].Off, File: old}, nil); code(err) != CodeStale {
		t.Fatalf("a full text of the old file: %v", err)
	}

	h := NewHostsDial([]fav.Host{{Name: "m"}}, "", func(fav.Host) (*Client, error) { return Pipe(NewLocal("t")), nil })
	defer h.Close()
	src := h.Source(&fav.Rec{Host: "m", Provider: s.Provider, SessionID: s.ID})
	if pg := src.Messages(-1, 2); pg.Err != nil {
		t.Fatal(pg.Err)
	}
	fileio.WriteAtomic(s.Path, 0o644, func(w io.Writer) error { _, err := w.Write(b); return err })
	if pg := src.Messages(-1, 2); !Stale(pg.Err) {
		t.Fatalf("the tail of a rewritten file says so once: %v", pg.Err)
	}
	if pg := src.Messages(-1, 2); pg.Err != nil || len(pg.Msgs) != 2 {
		t.Fatalf("then reads on: %v", pg.Err)
	}
	os.Remove(s.Path)
	if pg := src.Messages(-1, 2); code(pg.Err) != CodeNotFound {
		t.Fatalf("a transcript that cannot be read is an error, not an empty head: %v", pg.Err)
	}
}
