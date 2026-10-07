package remote

import (
	"context"
	"encoding/json"

	"github.com/oxsean/fav/internal/wire"
)

// env answers env and env.file.
func (h *localHandler) env(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
}
