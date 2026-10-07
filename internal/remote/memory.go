package remote

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/wire"
)

// memories answers memory.ls, memory.read, memory.trash, memory.restore and memory.put; a path outside the memory
// roots is unauthorized whether it exists or not.
func (h *localHandler) memories(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case MMemoryList:
		var p MemoryListParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
		}
		sets := memory.List(p.Dirs, p.Global)
		if sets == nil {
			sets = []memory.Set{}
		}
		return MemoryList{Sets: sets}, nil
	case MMemoryRead:
		var p MemoryFile
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
		}
		text, at, sha, err := memory.Read(p.File)
		if err != nil {
			return nil, memoryErr(err)
		}
		return MemoryText{Text: text, At: at, SHA: sha}, nil
	case MMemoryTrash:
		var p MemoryFile
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
		}
		id, err := memory.Trash(p.File)
		if err != nil {
			return nil, memoryErr(err)
		}
		return MemoryEntry{Entry: id}, nil
	case MMemoryRestore:
		var p MemoryEntry
		if err := json.Unmarshal(params, &p); err != nil || p.Entry == "" {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "entry"}
		}
		file, err := memory.Restore(p.Entry)
		if err != nil {
			return nil, &wire.Error{Code: wire.CodeConflict, Detail: err.Error()}
		}
		return MemoryFile{File: file}, nil
	case MMemoryPut:
		var p MemoryPutParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
		}
		if p.Kind != memory.KindClaude {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "kind"}
		}
		w, err := memory.Put(p.Dir, p.Name, []byte(p.Text), p.Line, p.Expect)
		var bad *memory.BadPut
		switch {
		case errors.Is(err, memory.ErrStale):
			return nil, &wire.Error{Code: wire.CodeStale, Detail: p.Name}
		case errors.As(err, &bad):
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: bad.Why}
		case err != nil:
			return nil, &wire.Error{Code: wire.CodeInternal, Detail: err.Error()}
		}
		return MemoryPut{File: w.File, Incoming: w.Incoming, Lines: w.Lines, Bytes: w.Bytes, Over: w.Over}, nil
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
}

func memoryErr(err error) error {
	if errors.Is(err, memory.ErrOutside) {
		return &wire.Error{Code: wire.CodeUnauthorized, Detail: "memory"}
	}
	return &wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}
}

// Memories lists name's memories of p.Dirs; an older tend is not asked.
func (h *Hosts) Memories(ctx context.Context, name string, p MemoryListParams) (MemoryList, error) {
	var res MemoryList
	err := h.callIfKnown(ctx, name, MMemoryList, p, &res)
	return res, err
}

// MemoryRead is one memory file on name.
func (h *Hosts) MemoryRead(ctx context.Context, name, file string) (MemoryText, error) {
	var res MemoryText
	err := h.callIfKnown(ctx, name, MMemoryRead, MemoryFile{File: file}, &res)
	return res, err
}

// MemoryTrash moves a memory on name into that machine's trash.
func (h *Hosts) MemoryTrash(ctx context.Context, name, file string) (MemoryEntry, error) {
	var res MemoryEntry
	err := h.callIfKnown(ctx, name, MMemoryTrash, MemoryFile{File: file}, &res)
	return res, err
}

// MemoryRestore puts a memory on name back from that machine's trash.
func (h *Hosts) MemoryRestore(ctx context.Context, name, entry string) (MemoryFile, error) {
	var res MemoryFile
	err := h.callIfKnown(ctx, name, MMemoryRestore, MemoryEntry{Entry: entry}, &res)
	return res, err
}

// MemoryPair is the memories of one project directory on two machines, compared: Dirs.From on the first, Dirs.To on
// the second.
type MemoryPair struct {
	Dirs DirPair           `json:"dirs"`
	From []memory.Set      `json:"from"`
	To   []memory.Set      `json:"to"`
	Diff memory.Comparison `json:"diff"`
}

// Swap is mp seen from the machine it was compared with.
func (mp MemoryPair) Swap() MemoryPair {
	out := MemoryPair{Dirs: DirPair{From: mp.Dirs.To, To: mp.Dirs.From}, From: mp.To, To: mp.From}
	for _, l := range []struct{ from, to *[]memory.Entry }{{&mp.Diff.OnlyHere, &out.Diff.OnlyThere}, {&mp.Diff.OnlyThere, &out.Diff.OnlyHere},
		{&mp.Diff.Differ, &out.Diff.Differ}, {&mp.Diff.Same, &out.Diff.Same}} {
		for _, e := range *l.from {
			*l.to = append(*l.to, e.Swap())
		}
	}
	return out
}

// CompareMemories lists each pair's memories on from and to (Codex's global blocks naming no directory with the first
// pair only) and compares them, the pair and the two homes the only paths mapped; texts are read only where hashes
// leave it open.
func CompareMemories(ctx context.Context, from, to Peer, pairs []DirPair) ([]MemoryPair, error) {
	var out []MemoryPair
	for i, p := range pairs {
		mp := MemoryPair{Dirs: p}
		for _, side := range []struct {
			peer Peer
			dir  string
			sets *[]memory.Set
		}{{from, p.From, &mp.From}, {to, p.To, &mp.To}} {
			var l MemoryList
			if err := side.peer.Call(ctx, MMemoryList, MemoryListParams{Dirs: []string{side.dir}, Global: true}, &l); err != nil {
				return nil, &PeerError{side.peer, err}
			}
			for _, s := range l.Sets {
				if i == 0 || s.Kind != memory.KindCodexGlobal || s.Dir != "" {
					*side.sets = append(*side.sets, s)
				}
			}
		}
		m := memory.Mapping{Here: from.End(), There: to.End(), Pairs: [][2]string{{p.From, p.To}}}
		mp.Diff = memory.Diff(mp.From, mp.To, m, memoryTexts(ctx, from, to))
		out = append(out, mp)
	}
	return out, nil
}

// memoryTexts reads items through memory.read on their machine, each file once; a Codex block is cut out of its file.
func memoryTexts(ctx context.Context, from, to Peer) memory.Texts {
	cache := map[[2]string]string{}
	return func(there bool, it memory.Item) (string, error) {
		p, side := from, "from"
		if there {
			p, side = to, "to"
		}
		k := [2]string{side, it.File}
		text, ok := cache[k]
		if !ok {
			var t MemoryText
			if err := p.Call(ctx, MMemoryRead, MemoryFile{File: it.File}, &t); err != nil {
				return "", &PeerError{p, err}
			}
			text, cache[k] = t.Text, t.Text
		}
		return it.In(text), nil
	}
}

// ReadMemory is it on p as text: its file, or the Codex block cut out of it.
func ReadMemory(ctx context.Context, p Peer, it memory.Item) (MemoryText, error) {
	var t MemoryText
	if err := p.Call(ctx, MMemoryRead, MemoryFile{File: it.File}, &t); err != nil {
		return MemoryText{}, err
	}
	t.Text = it.In(t.Text)
	return t, nil
}

// CopyMemory copies e, a Claude memory of mp's directory on from, to the directory on to with its MEMORY.md line,
// through memory.put: never over another, and refused (stale) when what to holds is no longer what mp saw.
func CopyMemory(ctx context.Context, from, to Peer, mp MemoryPair, e memory.Entry) (MemoryPut, error) {
	if e.Kind != memory.KindClaude || e.Here == nil {
		return MemoryPut{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "kind"}
	}
	if !to.Has(MMemoryPut) {
		return MemoryPut{}, &PeerError{to, &wire.Error{Code: wire.CodeUnknownMethod, Detail: to.Hello.Version}}
	}
	var t MemoryText
	if err := from.Call(ctx, MMemoryRead, MemoryFile{File: e.Here.File}, &t); err != nil {
		return MemoryPut{}, &PeerError{from, err}
	}
	line := ""
	if i := slices.IndexFunc(mp.From, func(s memory.Set) bool { return s.Kind == memory.KindClaude }); i >= 0 && e.Here.InIndex && mp.From[i].Index != "" {
		var idx MemoryText
		if err := from.Call(ctx, MMemoryRead, MemoryFile{File: mp.From[i].Index}, &idx); err != nil {
			return MemoryPut{}, &PeerError{from, err}
		}
		line = memory.IndexLine(idx.Text, e.Name)
	}
	p := MemoryPutParams{Dir: mp.Dirs.To, Kind: e.Kind, Name: e.Name, Text: t.Text, Line: line}
	if e.There != nil {
		p.Expect = e.There.SHA
	}
	var res MemoryPut
	if err := to.Call(ctx, MMemoryPut, p, &res); err != nil {
		return MemoryPut{}, &PeerError{to, err}
	}
	return res, nil
}

// PeerError is an error of the one machine of two that answered it.
type PeerError struct {
	Peer Peer
	Err  error
}

func (e *PeerError) Error() string { return e.Err.Error() }
func (e *PeerError) Unwrap() error { return e.Err }

// MemoryRefused says why name did not answer a memory method.
func MemoryRefused(host string, err error) string {
	if wire.Code(err) == wire.CodeUnknownMethod {
		return TooOld(host, MMemoryList)
	}
	return i18n.F("remote.memory_failed", host, Reason(err))
}

// MemoryRefusal is MemoryRefused for p, called host: a node sharing less than all its sessions keeps its memories.
func MemoryRefusal(p Peer, host string, err error) string {
	if share := ShareLimit(p, err); share != "" {
		return i18n.F("cli.memory.share", host, share)
	}
	return MemoryRefused(host, err)
}
