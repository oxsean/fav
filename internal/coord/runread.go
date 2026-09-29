package coord

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

type OutputItemParams struct {
	Run string `json:"run"`
	ID  string `json:"id"` // an event's id as a page gives it, or with its run: <run>/<file>:<off>:<n>
}

// OutputItem is one event of a run's output with nothing left out: its line read whole, the blobs it names put back
// while they fit in maxItemBlobs; Blobs are the ones left for run.blob.
type OutputItem struct {
	Event output.Event `json:"event"`
	Blobs []ItemBlob   `json:"blobs,omitempty"`
}

type ItemBlob struct {
	Blob  string `json:"blob"`
	Bytes int    `json:"bytes"`
}

type ProjectDirsParams struct {
	Project string `json:"project"`
	Machine string `json:"machine"`
	Path    string `json:"path,omitempty"` // "" lists where runs may go there
}

// ProjectDirs is a directory on a machine as someone who may run there sees it: whether it is there, and what is in
// it. Outside: the machine lets no run go there.
type ProjectDirs struct {
	Path    string     `json:"path,omitempty"`
	Exists  bool       `json:"exists"`
	Outside bool       `json:"outside,omitempty"`
	Parent  string     `json:"parent,omitempty"`
	Dirs    []node.Dir `json:"dirs,omitempty"`
}

// maxItemBlobs bounds the blob content an item puts back into its line.
var maxItemBlobs = 1 << 20

const (
	maxItemLine = 1 << 20
	maxFindHits = 200
)

// runCall calls method about run on its machine; a node that has no such method answers unsupported.
func (c *Coord) runCall(ctx context.Context, run task.Run, method string, params, out any) error {
	err := c.call(ctx, run.Machine, method, params, out)
	if wire.Code(err) == wire.CodeUnknownMethod {
		return &wire.Error{Code: wire.CodeUnsupported, Detail: method}
	}
	return err
}

// ownsMachine: p owns the machine run is on, and so sees everything that changed there; admins do not.
func (c *Coord) ownsMachine(p Principal, run task.Run) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ownerOf(run.Machine) == p.User
}

func (c *Coord) runChanges(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var cp node.ChangesParams
	if err := r.Decode(&cp); err != nil {
		return nil, err
	}
	run, err := c.readableRun(p, cp.Run)
	if err != nil {
		return nil, err
	}
	cp.All = c.ownsMachine(p, run)
	var out node.Changes
	return out, c.runCall(ctx, run, node.MRunChanges, cp, &out)
}

func (c *Coord) runDiff(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var dp node.DiffParams
	if err := r.Decode(&dp); err != nil {
		return nil, err
	}
	run, err := c.readableRun(p, dp.Run)
	if err != nil {
		return nil, err
	}
	dp.All = c.ownsMachine(p, run)
	var out node.Diff
	return out, c.runCall(ctx, run, node.MRunDiff, dp, &out)
}

func (c *Coord) runBlob(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var bp node.BlobParams
	if err := r.Decode(&bp); err != nil {
		return nil, err
	}
	run, err := c.readableRun(p, bp.Run)
	if err != nil {
		return nil, err
	}
	var out node.Blob
	return out, c.runCall(ctx, run, node.MRunBlob, bp, &out)
}

// outputFind looks in one run's output; the hits' ids carry the run, and next is the node's, for the next call.
func (c *Coord) outputFind(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var fp node.FindParams
	if err := r.Decode(&fp); err != nil {
		return nil, err
	}
	run, err := c.readableRun(p, fp.Run)
	if err != nil {
		return nil, err
	}
	fp.Limit = min(fp.Limit, maxFindHits)
	var out node.Found
	if err := c.runCall(ctx, run, node.MRunOutputFind, fp, &out); err != nil {
		return nil, err
	}
	for i := range out.Hits {
		out.Hits[i].ID = run.ID + "/" + out.Hits[i].ID
	}
	return out, nil
}

func (c *Coord) outputItem(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var ip OutputItemParams
	if err := r.Decode(&ip); err != nil {
		return nil, err
	}
	run, err := c.readableRun(p, ip.Run)
	if err != nil {
		return nil, err
	}
	id := ip.ID
	if before, after, ok := strings.Cut(id, "/"); ok {
		if before != run.ID {
			return nil, bad("id " + ip.ID)
		}
		id = after
	}
	file, off, n, ok := lineOf(id)
	if !ok {
		return nil, bad("id " + ip.ID)
	}
	var l node.Line
	if err := c.runCall(ctx, run, node.MRunLine, node.LineParams{Run: run.ID, File: file, Off: off, Max: maxItemLine}, &l); err != nil {
		return nil, err
	}
	if l.Size > int64(len(l.Text)) {
		if n != 0 {
			return nil, notFound(ip.ID)
		}
		return OutputItem{Event: output.Event{ID: id, Off: off, Kind: output.KindRaw, Text: l.Text,
			Truncated: map[string]int{"line": int(l.Size)}}}, nil
	}
	text, left := c.putBack(ctx, run, l.Text)
	evs := output.Whole(file, off, text)
	c.mu.Lock()
	said := c.said(run.ID)
	c.mu.Unlock()
	evs = output.Join(evs, nil, 1, said, map[string]bool{})
	for _, e := range evs {
		if e.ID == id {
			e.Turn = 0 // ⚠️ one line does not know its turn
			return OutputItem{Event: e, Blobs: left}, nil
		}
	}
	return nil, notFound(ip.ID)
}

// lineOf reads an event id <file>:<off>:<n>; a file id has colons of its own, and a mark's event (m<pos>) is no line's.
func lineOf(id string) (file string, off int64, n int, ok bool) {
	i := strings.LastIndexByte(id, ':')
	if i < 0 {
		return "", 0, 0, false
	}
	j := strings.LastIndexByte(id[:i], ':')
	if j <= 0 {
		return "", 0, 0, false
	}
	off, err1 := strconv.ParseInt(id[j+1:i], 10, 64)
	n, err2 := strconv.Atoi(id[i+1:])
	return id[:j], off, n, err1 == nil && err2 == nil && off >= 0 && n >= 0
}

// putBack is line with the blobs it names put back in place, while they fit in maxItemBlobs; left are the others.
func (c *Coord) putBack(ctx context.Context, run task.Run, line string) (string, []ItemBlob) {
	if !strings.Contains(line, `"$blob"`) {
		return line, nil
	}
	d := json.NewDecoder(strings.NewReader(line))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return line, nil
	}
	budget := maxItemBlobs
	var left []ItemBlob
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			if sha, ok := x["$blob"].(string); ok {
				size, _ := x["bytes"].(json.Number).Int64()
				if int(size) <= budget {
					if text, ok := c.blobText(ctx, run, sha); ok {
						budget -= len(text)
						return text
					}
				}
				left = append(left, ItemBlob{Blob: sha, Bytes: int(size)})
				return x
			}
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	v = walk(v)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return line, left
	}
	return b.String(), left
}

// blobText is all of blob sha of run, read page by page.
func (c *Coord) blobText(ctx context.Context, run task.Run, sha string) (string, bool) {
	var b strings.Builder
	for off := int64(0); ; {
		var page node.Blob
		if c.runCall(ctx, run, node.MRunBlob, node.BlobParams{Run: run.ID, Sha: sha, Off: off}, &page) != nil {
			return "", false
		}
		b.WriteString(page.Text)
		if page.Next <= off || b.Len() > maxItemBlobs {
			return b.String(), b.Len() <= maxItemBlobs
		}
		off = page.Next
	}
}

// projectDirs checks a directory on a machine for someone who may run p.Project's tasks there, and lists what is in
// it; without a path it lists where runs may go on the machine.
func (c *Coord) projectDirs(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var dp ProjectDirsParams
	if err := r.Decode(&dp); err != nil {
		return nil, err
	}
	c.mu.Lock()
	var err error
	switch {
	case dp.Project != "" && c.st.Projects[dp.Project] == nil, dp.Project != "" && roleIn(c.st, p, dp.Project) == "":
		err = notFound("project " + dp.Project)
	case dp.Project != "" && roleIn(c.st, p, dp.Project) != task.RoleParticipant:
		err = forbidden(MProjectDirs)
	case c.ms[dp.Machine] == nil || !c.canSee(p, dp.Machine):
		err = notFound("machine " + dp.Machine)
	case !c.canUse(p, dp.Machine, dp.Project):
		err = forbidden(MProjectDirs)
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var ds node.Dirs
	switch err := c.call(ctx, dp.Machine, node.MDirs, node.DirsParams{Path: dp.Path}, &ds); wire.Code(err) {
	case "":
		return ProjectDirs{Path: ds.Path, Exists: true, Parent: ds.Parent, Dirs: ds.Dirs}, nil
	case wire.CodeNotFound:
		return ProjectDirs{Path: dp.Path}, nil
	case wire.CodeUnauthorized:
		return ProjectDirs{Path: dp.Path, Outside: true}, nil
	default:
		return nil, err
	}
}
