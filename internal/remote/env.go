package remote

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/wire"
)

// EnvText is env.file's answer: one instruction file's text.
type EnvText struct {
	Text string `json:"text"`
}

// env answers env and env.file.
func (h *localHandler) env(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == MEnvFile {
		var p EnvFileParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if p.Dir != "" && !filepath.IsAbs(p.Dir) {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "dir"}
		}
		text, err := envcheck.FileText(p.Kind, p.Name, p.Dir)
		if errors.Is(err, envcheck.ErrNotListed) || errors.Is(err, os.ErrNotExist) {
			return nil, &wire.Error{Code: wire.CodeNotFound, Detail: p.Kind + ":" + p.Name}
		}
		if err != nil {
			return nil, err
		}
		return EnvText{Text: text}, nil
	}
	var p EnvParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	dir := p.Dir
	var seen *envcheck.Seen
	if p.Ref != nil {
		if p.Ref.Provider == "" || p.Ref.SessionID == "" {
			return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "ref"}
		}
		h.mu.Lock()
		r, err := h.lookup(*p.Ref, queryFresh)
		var e *index.Env
		if err == nil {
			e = h.idx.Env(r.Provider, r.SessionID)
		}
		h.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if dir == "" {
			dir = r.Cwd
		}
		seen = envcheck.SeenOf(r.Provider, e, dir)
	}
	if dir != "" && !filepath.IsAbs(dir) {
		return nil, &wire.Error{Code: wire.CodeBadRequest, Detail: "dir"}
	}
	pr := envcheck.Collect(ctx, dir)
	pr.Seen = seen
	return pr, nil
}
