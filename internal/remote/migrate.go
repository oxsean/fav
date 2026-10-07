package remote

import (
	"context"
	"encoding/json"

	"github.com/oxsean/fav/internal/wire"
)

// migration answers export.plan, export.read, export.done, copies and import.*.
func (h *localHandler) migration(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
}
