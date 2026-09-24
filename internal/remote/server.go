package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Handler answers one request; an error that is not an *Error goes back as CodeInternal.
type Handler interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
}

// Serve answers requests from in, one JSON line each, until in ends; once stops after the first.
func Serve(ctx context.Context, in io.Reader, out io.Writer, h Handler, once bool) error {
	r := bufio.NewReaderSize(in, 1<<20)
	enc := json.NewEncoder(out)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if werr := enc.Encode(answer(ctx, line, h)); werr != nil {
				return werr
			}
			if once {
				return nil
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func answer(ctx context.Context, line []byte, h Handler) Response {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Response{Error: &Error{Code: CodeBadRequest, Detail: err.Error()}}
	}
	if req.Proto != Proto && req.Method != MHello {
		return Response{ID: req.ID, Error: &Error{Code: CodeProto}}
	}
	res, err := h.Handle(ctx, req.Method, req.Params)
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: CodeInternal, Detail: err.Error()}
		}
		return Response{ID: req.ID, Error: e}
	}
	b, err := json.Marshal(res)
	if err != nil {
		return Response{ID: req.ID, Error: &Error{Code: CodeInternal, Detail: err.Error()}}
	}
	return Response{ID: req.ID, OK: true, Result: b}
}

// Pipe is a Client served by h in this process.
func Pipe(h Handler) *Client {
	reqR, reqW := io.Pipe()
	resR, resW := io.Pipe()
	go func() {
		Serve(context.Background(), reqR, resW, h, false)
		resW.Close()
	}()
	return NewClient(resR, reqW)
}
