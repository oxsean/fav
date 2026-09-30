package coord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// farRun is a run bob dispatched on far, in p1, ended.
func farRun(t *testing.T, f *far) (*env, task.Run) {
	e := team(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	e.project()
	tk := e.taskAs(bob, "b1", "p1", "quick")
	var r task.Run
	if err := callAs(e.as(bob), MRunDispatch, "d1", Dispatch{Task: tk.ID, Machine: "far"}, &r); err != nil {
		t.Fatal(err)
	}
	e.wait(r.ID, ended)
	return e, r
}

func TestOnlyTheMachinesOwnerSeesEveryChangeOfARun(t *testing.T) {
	f := newFar(t)
	e, r := farRun(t, f)
	for _, c := range []struct {
		who  Principal
		all  bool
		what string
	}{
		{ann, true, "the machine's owner"},
		{bob, false, "a participant, though he asks for all"},
		{root, false, "an admin who does not own the machine"},
	} {
		before := len(f.calls(MRunChanges))
		err := callAs(e.as(c.who), MRunChanges, "", node.ChangesParams{Run: r.ID, All: true}, nil)
		if wire.Code(err) == wire.CodeNotFound || wire.Code(err) == wire.CodeUnauthorized {
			t.Fatalf("%s: %v", c.what, err)
		}
		if err := callAs(e.as(c.who), MRunDiff, "", node.DiffParams{Run: r.ID, Path: "a.txt", All: true, IgnoreSpace: true}, nil); wire.Code(err) == wire.CodeUnauthorized {
			t.Fatalf("%s: %v", c.what, err)
		}
		asked := f.calls(MRunChanges)
		diffs := f.calls(MRunDiff)
		if len(asked) != before+1 || len(diffs) == 0 {
			t.Fatalf("%s: forwarded %d", c.what, len(asked)-before)
		}
		var cp node.ChangesParams
		var dp node.DiffParams
		json.Unmarshal(asked[len(asked)-1], &cp)
		json.Unmarshal(diffs[len(diffs)-1], &dp)
		if cp.All != c.all || dp.All != c.all || cp.Run != r.ID || !dp.IgnoreSpace {
			t.Errorf("%s: changes %+v, diff %+v", c.what, cp, dp)
		}
	}
	n := len(f.calls(MRunChanges))
	for _, m := range []string{MRunChanges, MRunDiff, MRunBlob, MRunOutputFind, MRunOutputItem} {
		if err := callAs(e.as(cy), m, "", map[string]any{"run": r.ID, "path": "a.txt", "sha": strings.Repeat("0", 64), "q": "x", "id": "f:0:0"}, nil); wire.Code(err) != wire.CodeNotFound {
			t.Errorf("%s by someone outside the project: %v", m, err)
		}
	}
	if len(f.calls(MRunChanges)) != n {
		t.Error("nothing is forwarded for someone who may not read the run")
	}
}

// A diff ignoring whitespace goes only to a node that has the feature: an older one would answer the plain diff.
func TestADiffIgnoringSpaceNeedsTheNodesFeature(t *testing.T) {
	old := node.Features
	node.Features = slices.DeleteFunc(slices.Clone(old), func(f string) bool { return f == node.FeatureIgnoreSpace })
	t.Cleanup(func() { node.Features = old })
	f := newFar(t)
	e, r := farRun(t, f)
	before := len(f.calls(MRunDiff))
	err := callAs(e.as(bob), MRunDiff, "", node.DiffParams{Run: r.ID, Path: "a.txt", IgnoreSpace: true}, nil)
	if wire.Code(err) != wire.CodeUnsupported || err.(*wire.Error).Detail != node.FeatureIgnoreSpace {
		t.Fatalf("%v", err)
	}
	if len(f.calls(MRunDiff)) != before {
		t.Fatal("nothing is forwarded")
	}
	callAs(e.as(bob), MRunDiff, "", node.DiffParams{Run: r.ID, Path: "a.txt"}, nil)
	if len(f.calls(MRunDiff)) != before+1 {
		t.Fatal("the plain diff is")
	}
}

func TestANodeWithoutAMethodAnswersUnsupported(t *testing.T) {
	f := newFar(t)
	e, r := farRun(t, f)
	f.mu.Lock()
	f.lacks = []string{node.MRunChanges, node.MRunOutputFind}
	f.mu.Unlock()
	err := callAs(e.as(bob), MRunChanges, "", node.ChangesParams{Run: r.ID}, nil)
	if wire.Code(err) != wire.CodeUnsupported || err.(*wire.Error).Detail != MRunChanges {
		t.Fatalf("%v", err)
	}
	if err := callAs(e.as(bob), MRunOutputFind, "", node.FindParams{Run: r.ID, Q: "x", Before: -1}, nil); wire.Code(err) != wire.CodeUnsupported {
		t.Fatalf("%v", err)
	}
}

func TestFindHitsCarryTheirRunAndTheNodesCursor(t *testing.T) {
	f := newFar(t)
	e, r := farRun(t, f)
	var log strings.Builder
	for i := range 5 {
		log.WriteString(`{"type":"assistant","message":{"content":[{"type":"text","text":"needle ` + strconv.Itoa(i) + `"}]}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(f.home, "node", "runs", r.ID, "output.log"), []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	var got node.Found
	if err := callAs(e.as(bob), MRunOutputFind, "", node.FindParams{Run: r.ID, Q: "NEEDLE", Before: -1, Limit: 5000}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 5 || got.File == "" {
		t.Fatalf("%+v", got)
	}
	for _, h := range got.Hits {
		if !strings.HasPrefix(h.ID, r.ID+"/"+got.File+":") {
			t.Errorf("a hit's id names its run: %q", h.ID)
		}
	}
	asked := f.calls(MRunOutputFind)
	var fp node.FindParams
	json.Unmarshal(asked[len(asked)-1], &fp)
	if fp.Limit != maxFindHits {
		t.Errorf("the limit the node gets: %d", fp.Limit)
	}
}

func TestAnItemIsItsLineWholeWithItsBlobsPutBack(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID})
	e.wait(r.ID, ended)
	dir := filepath.Join(e.home, "node", "runs", r.ID)
	content := strings.Repeat("new line\n", 50)
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	os.MkdirAll(filepath.Join(dir, "blobs"), 0o700)
	if err := os.WriteFile(filepath.Join(dir, "blobs", sha), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var long []string
	for i := range 200 {
		long = append(long, "out "+strconv.Itoa(i))
	}
	out, _ := json.Marshal(strings.Join(long, "\n"))
	log := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"a.txt","content":{"$blob":"` + sha + `","bytes":` + strconv.Itoa(len(content)) + `,"lines":50}}}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"w1","content":` + string(out) + `}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "marks.jsonl"))
	var page OutputPage
	e.must(MRunOutputPage, OutputPageParams{Run: r.ID, Before: -1}, &page)
	var write, result output.Event
	for _, ev := range page.Events {
		switch ev.Kind {
		case output.KindTool:
			write = ev
		case output.KindToolResult:
			result = ev
		}
	}
	if result.Truncated["output"] == 0 {
		t.Fatalf("the page cuts the output: %+v", result)
	}

	var item OutputItem
	e.must(MRunOutputItem, OutputItemParams{Run: r.ID, ID: r.ID + "/" + result.ID}, &item)
	if item.Event.ID != result.ID || item.Event.Output != strings.Join(long, "\n") || item.Event.Truncated != nil || item.Event.Ref != "w1" {
		t.Fatalf("the whole result: %+v", item.Event)
	}
	e.must(MRunOutputItem, OutputItemParams{Run: r.ID, ID: write.ID}, &item)
	var in struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(item.Event.Input, &in); in.Content != content || len(item.Blobs) != 0 {
		t.Fatalf("the blob is put back: %s %+v", item.Event.Input, item.Blobs)
	}

	defer func(was int) { maxItemBlobs = was }(maxItemBlobs)
	maxItemBlobs = 100
	e.must(MRunOutputItem, OutputItemParams{Run: r.ID, ID: write.ID}, &item)
	if len(item.Blobs) != 1 || item.Blobs[0].Blob != sha || item.Blobs[0].Bytes != len(content) || strings.Contains(string(item.Event.Input), "new line") {
		t.Fatalf("a blob past the bound is left for run.blob: %s %+v", item.Event.Input, item.Blobs)
	}

	for _, id := range []string{"r_other/" + write.ID, "nonsense", write.ID[:strings.LastIndexByte(write.ID, ':')] + ":m0"} {
		if err := e.call(MRunOutputItem, OutputItemParams{Run: r.ID, ID: id}, nil); wire.Code(err) != wire.CodeBadRequest {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if err := e.call(MRunOutputItem, OutputItemParams{Run: r.ID, ID: write.ID[:strings.LastIndexByte(write.ID, ':')] + ":7"}, nil); wire.Code(err) != wire.CodeNotFound {
		t.Errorf("no such event in the line: %v", err)
	}
}

func TestProjectDirsIsForWhomMayRunTheProjectsTasksThere(t *testing.T) {
	f := newFar(t)
	f.set(nil)
	e := team(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	e.project()
	var roots ProjectDirs
	if err := callAs(e.as(bob), MProjectDirs, "", ProjectDirsParams{Project: "p1", Machine: "far"}, &roots); err != nil || !roots.Exists || len(roots.Dirs) == 0 {
		t.Fatalf("where runs may go: %+v %v", roots, err)
	}
	root := roots.Dirs[0].Path
	sub := filepath.Join(root, "work")
	os.MkdirAll(filepath.Join(sub, "app"), 0o700)
	var got ProjectDirs
	if err := callAs(e.as(bob), MProjectDirs, "", ProjectDirsParams{Project: "p1", Machine: "far", Path: sub}, &got); err != nil || !got.Exists || len(got.Dirs) != 1 || got.Dirs[0].Name != "app" {
		t.Fatalf("a directory there: %+v %v", got, err)
	}
	if err := callAs(e.as(bob), MProjectDirs, "", ProjectDirsParams{Project: "p1", Machine: "far", Path: filepath.Join(sub, "missing")}, &got); err != nil || got.Exists || got.Outside {
		t.Fatalf("a directory not there: %+v %v", got, err)
	}
	outside := filepath.Dir(root)
	if err := callAs(e.as(bob), MProjectDirs, "", ProjectDirsParams{Project: "p1", Machine: "far", Path: outside}, &got); err != nil || got.Exists || !got.Outside {
		t.Fatalf("outside where runs may go: %+v %v", got, err)
	}
	for _, c := range []struct {
		who     Principal
		project string
		code    string
	}{
		{dee, "p1", wire.CodeUnauthorized}, // reads p1
		{cy, "p1", wire.CodeNotFound},      // not in p1
		{cy, "", wire.CodeNotFound},        // far is not shared with her
		{bob, "", wire.CodeUnauthorized},   // far is shared with p1, not with him
		{bob, "p9", wire.CodeNotFound},     // no such project
	} {
		if err := callAs(e.as(c.who), MProjectDirs, "", ProjectDirsParams{Project: c.project, Machine: "far", Path: sub}, nil); wire.Code(err) != c.code {
			t.Errorf("%s in %q: %v", c.who.User, c.project, err)
		}
	}
	if err := callAs(e.as(ann), MProjectDirs, "", ProjectDirsParams{Machine: "far", Path: sub}, nil); err != nil {
		t.Errorf("the machine's owner, outside any project: %v", err)
	}
}
