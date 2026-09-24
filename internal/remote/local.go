package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
)

// NewLocal is this machine answering the protocol (`fav rpc`); version is fav's build version.
func NewLocal(version string) Handler { return &localHandler{version: version} }

type localHandler struct {
	version string
	mu      sync.Mutex
	store   *fav.Store
	idx     *index.Index
	unfav   []*fav.Rec
}

var methods = []string{MHello, MList, MMessages, MText, MSteps, MPulse, MChecks, MLive, MEcho}

func (h *localHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case MHello:
		var p HelloParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if p.Lang != "" {
			i18n.Set(p.Lang)
		}
		return hello(h.version), nil
	case MList:
		return h.list()
	case MLive:
		return Live{Live: capture.LiveSessions()}, nil
	case MEcho:
		var t Text
		err := decode(params, &t)
		return t, err
	case MMessages:
		var p MessagesParams
		src, err := h.source(params, &p, &p.Ref)
		if err != nil {
			return nil, err
		}
		if p.N <= 0 {
			return nil, &Error{Code: CodeBadRequest, Detail: "n"}
		}
		id, err := sameFile(src, p.File)
		if err != nil {
			return nil, err
		}
		page := src.Messages(p.Before, p.N)
		page.File = id
		return page, nil
	case MText:
		var p TextParams
		src, err := h.source(params, &p, &p.Ref)
		if err != nil {
			return nil, err
		}
		if _, err := sameFile(src, p.File); err != nil {
			return nil, err
		}
		return Text{Text: src.TextFull(p.Off, p.Fallback)}, nil
	case MSteps:
		var p StepsParams
		src, err := h.source(params, &p, &p.Ref)
		if err != nil {
			return nil, err
		}
		if _, err := sameFile(src, p.File); err != nil {
			return nil, err
		}
		return Steps{Texts: src.StepsFull(p.Steps)}, nil
	case MPulse:
		var p Ref
		src, err := h.source(params, &p, &p)
		if err != nil {
			return nil, err
		}
		pulse, ok := src.Pulse()
		return PulseResult{Pulse: pulse, OK: ok}, nil
	case MChecks:
		var p Ref
		src, err := h.source(params, &p, &p)
		if err != nil {
			return nil, err
		}
		return Checks{Checks: src.Checks()}, nil
	}
	return nil, &Error{Code: CodeUnknownMethod, Detail: method}
}

// sameFile is the id of src's transcript; not_found when it cannot be read (not the head of an empty file), stale when
// it is not the file want names.
func sameFile(src Source, want string) (string, error) {
	path := ""
	if l, ok := src.(local); ok {
		path = l.path()
	}
	id := fileio.ID(path)
	switch {
	case id == "":
		return "", &Error{Code: CodeNotFound, Detail: "transcript"}
	case want != "" && want != id:
		return "", &Error{Code: CodeStale}
	}
	return id, nil
}

func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return &Error{Code: CodeBadRequest, Detail: err.Error()}
	}
	return nil
}

func hello(version string) Hello {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	wsl := os.Getenv("WSL_DISTRO_NAME")
	claude, codex := capture.ClaudeHome(), capture.CodexHome()
	sum := sha256.Sum256([]byte(strings.Join([]string{host, runtime.GOOS, wsl, fav.Home(), claude, codex}, "\x00")))
	return Hello{Proto: Proto, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Endpoint: hex.EncodeToString(sum[:6]), Hostname: host, WSL: wsl, Home: home, Sep: string(filepath.Separator),
		Claude: claude, Codex: codex, Methods: methods,
		CLIs: map[string]bool{fav.ProviderClaude: capture.Installed(fav.ProviderClaude), fav.ProviderCodex: capture.Installed(fav.ProviderCodex)}}
}

// load opens the store and the index once, then brings both up to date; the caller holds mu.
func (h *localHandler) load() error {
	if h.store == nil {
		s, err := fav.Open()
		if err != nil {
			return err
		}
		idx, err := index.Open()
		if err != nil {
			return err
		}
		h.store, h.idx = s, idx
	} else if h.store.Changed() {
		if err := h.store.Reload(); err != nil {
			return err
		}
	}
	idx, changed := h.idx.Refresh()
	if changed {
		if err := idx.Save(); err != nil {
			fmt.Fprintln(os.Stderr, i18n.F("cli.index_not_written", err))
		}
	}
	h.idx = idx
	h.unfav = idx.Attach(h.store, h.unfav)
	return nil
}

// list: every session the TUI could list here (favorites, unfavorited, archived; not agent runs), whatever the filters.
func (h *localHandler) list() (List, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.load(); err != nil {
		return List{}, err
	}
	var rows index.Rows
	recs, err := rows.List(h.store, h.idx, h.unfav, nil, fav.Query{Status: "all", All: true, Host: fav.HostLocal})
	if err != nil {
		return List{}, err
	}
	out := List{Sessions: make([]Session, len(recs))}
	for i, r := range recs {
		out.Sessions[i] = SessionOf(r)
	}
	return out, nil
}

// source decodes params into p and resolves its ref to this machine's record.
func (h *localHandler) source(params json.RawMessage, p any, ref *Ref) (Source, error) {
	if err := decode(params, p); err != nil {
		return nil, err
	}
	if ref.Provider == "" || ref.SessionID == "" {
		return nil, &Error{Code: CodeBadRequest, Detail: "ref"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.store == nil {
		if err := h.load(); err != nil {
			return nil, err
		}
	}
	r := h.find(*ref)
	if r == nil { // started or favorited since the last load
		if err := h.load(); err != nil {
			return nil, err
		}
		r = h.find(*ref)
	}
	if r == nil {
		return nil, &Error{Code: CodeNotFound, Detail: ref.Provider + ":" + ref.SessionID}
	}
	return Local(r), nil
}

// find: the store record, else the indexed session, else a file the list leaves out (an agent run); the caller holds mu.
func (h *localHandler) find(ref Ref) *fav.Rec {
	if r := h.store.BySession(ref.Provider, ref.SessionID); r != nil && !r.Deleted {
		return r
	}
	for _, r := range h.unfav {
		if r.Provider == ref.Provider && r.SessionID == ref.SessionID {
			return r
		}
	}
	if f, _ := h.idx.FileByPrefix(ref.SessionID); f != nil && f.Provider == ref.Provider && f.SessionID == ref.SessionID {
		return f.Rec()
	}
	return nil
}
