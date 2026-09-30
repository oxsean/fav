package coord

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// sameAsSave checks text as who, then saves it: a check without errors is a save that is taken, one with errors is a
// save refused with them, and a check refused is a save refused the same way.
func sameAsSave(t *testing.T, e *env, who Principal, save AgentDefSave, id string) AgentDefCheck {
	t.Helper()
	var got AgentDefCheck
	cerr := callAs(e.as(who), MAgentDefCheck, "", save, &got)
	var v AgentDefView
	serr := callAs(e.as(who), MAgentDefSave, id, save, &v)
	switch {
	case cerr != nil:
		if wire.Code(serr) != wire.Code(cerr) {
			t.Fatalf("the check refuses (%v), the save does not: %v", cerr, serr)
		}
	case len(got.Errors) > 0:
		var we *wire.Error
		if serr == nil || wire.Code(serr) != wire.CodeBadRequest || !asWire(serr, &we) || we.Detail != strings.Join(got.Errors, "; ") {
			t.Fatalf("the check says %q, the save: %v", got.Errors, serr)
		}
	default:
		if serr != nil || !slices.Equal(v.Warnings, got.Warnings) || v.Name != got.Name || !slices.Equal(v.Launch, got.Launch) {
			t.Fatalf("the check (%+v) says what the save does (%+v %v)", got, v, serr)
		}
	}
	return got
}

func asWire(err error, out **wire.Error) bool {
	we, ok := err.(*wire.Error)
	*out = we
	return ok
}

// A check says what saving the text would, and saves nothing.
func TestACheckSaysWhatSavingWouldAndSavesNothing(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	text := "---\nname: wide\nprofile: quick\ntools: {allow: [Bash]}\nshade: blue\n---\nWork.\n"
	var got AgentDefCheck
	e.must(MAgentDefCheck, AgentDefSave{Text: text}, &got)
	if got.Name != "wide" || len(got.Errors) != 0 || len(got.Warnings) != 2 || len(got.Launch) == 0 {
		t.Fatalf("its name, two warnings and what a run starts: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(e.home, "defs", "agents", "wide.md")); err == nil {
		t.Fatal("a check wrote the definition")
	}
	var list AgentDefList
	e.must(MAgentDefList, nil, &list)
	if len(list.Defs) != 0 {
		t.Fatalf("nothing is saved: %+v", list.Defs)
	}
	for i, text := range []string{
		text,
		"---\nname: Bad Name\nrole: boss\n---\n",
		"---\nname: [unclosed\n---\n",
		"no front matter at all",
		"---\nname: nobase\nprofile: missing\n---\n",
	} {
		sameAsSave(t, e, Owner, AgentDefSave{Text: text}, "s"+itoa(int64(i)))
	}
	var bad AgentDefCheck
	e.must(MAgentDefCheck, AgentDefSave{Text: "---\nname: Bad Name\nrole: boss\n---\n"}, &bad)
	if len(bad.Errors) != 3 {
		t.Fatalf("every problem is listed, not only the first: %+v", bad)
	}
}

// A check holds whoever asks to the rules of saving: another's definition, a project they do not run.
func TestACheckIsRefusedWhereTheSaveWouldBe(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	anns := "---\nname: anns\nprofile: quick\n---\nMine.\n"
	var v AgentDefView
	if err := callAs(e.as(ann), MAgentDefSave, "a1", AgentDefSave{Text: anns}, &v); err != nil {
		t.Fatal(err)
	}
	seq := e.c.State().Seq
	if err := callAs(e.as(cy), MAgentDefCheck, "", AgentDefSave{Text: anns}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Fatalf("a definition he cannot see is not there for him: %v", err)
	}
	var got AgentDefCheck
	if err := callAs(e.as(ann), MAgentDefCheck, "", AgentDefSave{Text: anns}, &got); err != nil || e.c.State().Seq != seq {
		t.Fatalf("checking writes nothing to the journal: %v, seq %d then %d", err, seq, e.c.State().Seq)
	}
	for i, c := range []struct {
		who  Principal
		save AgentDefSave
	}{
		{bob, AgentDefSave{Text: "---\nname: bobs\nprofile: quick\n---\n", Owner: "project:p1"}},
		{bob, AgentDefSave{Text: "---\nname: bobs2\nprofile: quick\n---\n", Owner: "project:nope"}},
		{ann, AgentDefSave{Text: "---\nname: shop\nprofile: quick\n---\n", Owner: "project:p1"}},
		{cy, AgentDefSave{Text: anns}},
	} {
		sameAsSave(t, e, c.who, c.save, "c"+itoa(int64(i)))
	}
	if err := callAs(e.as(ann), MAgentDefShare, "a2", map[string]any{"name": "anns", "share": map[string]any{"users": []string{bob.User}, "view": true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(bob), MAgentDefCheck, "", AgentDefSave{Text: anns}, nil); wire.Code(err) != wire.CodeUnauthorized {
		t.Fatalf("he uses it but does not manage it: %v", err)
	}
}
