package remote

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/wire"
)

// memories answers memory.ls, memory.read, memory.trash and memory.restore; a path outside the memory roots is
// unauthorized whether it exists or not.
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

// MemoryRefused says why name did not answer a memory method.
func MemoryRefused(host string, err error) string {
	if wire.Code(err) == wire.CodeUnknownMethod {
		return i18n.F("remote.memory_old", host, host)
	}
	return i18n.F("remote.memory_failed", host, Reason(err))
}
