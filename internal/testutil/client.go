package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http/middleware"
)

// Client is an HTTP client with the token already in the header. Every method
// fails the test on a transport error rather than returning one: a test that
// cannot reach the server has nothing left to assert.
type Client struct {
	t     *testing.T
	base  string
	token string
	opID  string
	hc    *http.Client
}

func newClient(t *testing.T, base, token string) *Client {
	return &Client{t: t, base: base, token: token, hc: base_client()}
}

func base_client() *http.Client {
	return &http.Client{
		// Redirects would hide a routing mistake behind a second request.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NoAuth returns a client that sends no Authorization header, for the C2 case
// that every endpoint has.
func (c *Client) NoAuth() *Client {
	clone := *c
	clone.token = ""
	return &clone
}

// WithToken returns a client sending a different token, for C3.
func (c *Client) WithToken(token string) *Client {
	clone := *c
	clone.token = token
	return &clone
}

// WithOpID returns a client that stamps X-Op-Id on every request, so a test
// can assert the id came back on the event.
func (c *Client) WithOpID(opID string) *Client {
	clone := *c
	clone.opID = opID
	return &clone
}

// Response is what the assertions read.
type Response struct {
	t      *testing.T
	Status int
	Body   []byte
	Header http.Header
}

func (c *Client) GET(path string) *Response    { return c.Do(http.MethodGet, path, nil) }
func (c *Client) DELETE(path string) *Response { return c.Do(http.MethodDelete, path, nil) }

func (c *Client) POST(path string, body any) *Response {
	return c.Do(http.MethodPost, path, body)
}

func (c *Client) PATCH(path string, body any) *Response {
	return c.Do(http.MethodPatch, path, body)
}

func (c *Client) PUT(path string, body any) *Response {
	return c.Do(http.MethodPut, path, body)
}

// Do sends a request. A string body is sent verbatim, which is how the
// malformed-JSON cases are written; anything else is marshalled.
func (c *Client) Do(method, path string, body any) *Response {
	c.t.Helper()

	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = bytes.NewBufferString(b)
	case []byte:
		reader = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			c.t.Fatalf("marshalling the request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("building the request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		// The same header the browser uses. Not Authorization: that one
		// belongs to nginx's basic auth in production, and a request carries
		// only one of them (see middleware.TokenHeader).
		req.Header.Set(middleware.TokenHeader, c.token)
	}
	if c.opID != "" {
		req.Header.Set("X-Op-Id", c.opID)
	}

	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatalf("reading the response body: %v", err)
	}
	return &Response{t: c.t, Status: res.StatusCode, Body: raw, Header: res.Header}
}

// ExpectStatus fails the test unless the status matches, printing the body —
// which is almost always what explains the difference.
func (r *Response) ExpectStatus(want int) *Response {
	r.t.Helper()

	if r.Status != want {
		r.t.Fatalf("status = %d, want %d\nbody: %s", r.Status, want, r.Body)
	}
	return r
}

// JSON decodes the body into out.
func (r *Response) JSON(out any) *Response {
	r.t.Helper()

	if err := json.Unmarshal(r.Body, out); err != nil {
		r.t.Fatalf("decoding the response: %v\nbody: %s", err, r.Body)
	}
	return r
}

// ErrorCode returns the machine-readable code from an error response.
func (r *Response) ErrorCode() string {
	r.t.Helper()

	var body struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil {
		r.t.Fatalf("decoding the error response: %v\nbody: %s", err, r.Body)
	}
	return body.Error.Code
}
