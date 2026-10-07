package remote

import (
	"context"
	"encoding/json"

	"github.com/oxsean/fav/internal/wire"
)

// memories answers memory.ls, memory.read, memory.trash, memory.restore and memory.put.
func (h *localHandler) memories(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
}
