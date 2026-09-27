package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// rest is a tracker's REST API rooted at api, with its own way to carry the token.
type rest struct {
	cfg    Config
	api    string
	auth   func(http.Header)
	accept string
}

// do sends a request with a JSON body (when in is not nil) and decodes a JSON answer into out (when not nil).
func (g *rest) do(ctx context.Context, method, path string, header http.Header, in, out any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.api+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	g.auth(req.Header)
	req.Header.Set("Accept", g.accept)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode == http.StatusNotModified {
		return res, nil
	}
	if res.StatusCode/100 != 2 {
		return res, statusError(res, b, time.Now())
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return res, fmt.Errorf("tracker answer: %w", err)
		}
	}
	return res, nil
}
