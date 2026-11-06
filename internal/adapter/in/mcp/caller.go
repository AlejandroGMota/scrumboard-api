package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http/middleware"
)

// Caller is how the tools reach the board. Both implementations speak to the
// same HTTP surface the browser uses, so validation, WIP limits and the
// WebSocket broadcast apply to Claude exactly as they do to a person.
type Caller interface {
	Call(ctx context.Context, method, path string, body any) (json.RawMessage, error)
}

// APIError is a non-2xx answer from the API, kept whole so a tool can report
// it to Claude as the API phrased it.
type APIError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Body)
}

// HTTPCaller calls a running API over the network. It is what the stdio
// server on a laptop uses to reach the deployed board.
type HTTPCaller struct {
	Base   string
	Token  string
	Client *http.Client
}

func (c *HTTPCaller) Call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	req, err := newRequest(ctx, method, c.Base+path, c.Token, body)
	if err != nil {
		return nil, err
	}
	res, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return result(method, path, res.StatusCode, raw)
}

// Loopback calls the API's own handler in-process. It is what the remote
// connector uses: the request never leaves the process, but it still passes
// through the router, the token check and every middleware.
//
// Handler is set after construction, because the router that serves the
// connector is also the handler the connector calls.
type Loopback struct {
	Handler http.Handler
	Token   string
}

func (l *Loopback) Call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	req, err := newRequest(ctx, method, "http://loopback"+path, l.Token, body)
	if err != nil {
		return nil, err
	}
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	l.Handler.ServeHTTP(rec, req)
	return result(method, path, rec.status, rec.body.Bytes())
}

func newRequest(ctx context.Context, method, url, token string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set(middleware.TokenHeader, token)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func result(method, path string, status int, raw []byte) (json.RawMessage, error) {
	if status >= 300 {
		return nil, &APIError{Method: method, Path: path, Status: status, Body: strings.TrimSpace(string(raw))}
	}
	if len(raw) == 0 {
		raw = []byte(`{"ok":true}`)
	}
	return raw, nil
}

// recorder is the smallest ResponseWriter that keeps what the handler wrote.
type recorder struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
}

func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}
