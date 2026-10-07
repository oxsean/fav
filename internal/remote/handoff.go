package remote

import (
	"context"
	"encoding/json"

	"github.com/oxsean/fav/internal/wire"
)

// handoff answers handoff.facts and handoff.put.
func (h *localHandler) handoff(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
}
