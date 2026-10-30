// Command mcp is a Model Context Protocol server over stdio that lets Claude
// read and edit the board through the public HTTP API. It holds no state of
// its own: every tool call is one request to scrumboard-api.
//
// Configuration:
//
//	SCRUMBOARD_URL    base URL, e.g. https://scrumboard.alejandrogmota.com (default http://localhost:8020)
//	SCRUMBOARD_TOKEN  the same token the API checks in X-Scrumboard-Token
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

const protocolVersion = "2025-06-18"

type client struct {
	base  string
	token string
	http  *http.Client
}

func (c *client) call(method, path string, body any) (json.RawMessage, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Scrumboard-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{"ok":true}`)
	}
	return raw, nil
}

// --- tools ----------------------------------------------------------------

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	run         func(c *client, a map[string]any) (json.RawMessage, error)
}

func obj(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func arg(a map[string]any, k string) (string, error) {
	v, ok := a[k].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("missing argument %q", k)
	}
	return v, nil
}

var tools = []tool{
	{
		Name:        "list_boards",
		Description: "List every board (id and name).",
		InputSchema: obj(nil, map[string]any{}),
		run: func(c *client, _ map[string]any) (json.RawMessage, error) {
			return c.call("GET", "/api/boards", nil)
		},
	},
	{
		Name:        "get_board",
		Description: "Read one board with its columns and cards, in board order. Use it to find column and card ids.",
		InputSchema: obj([]string{"board_id"}, map[string]any{"board_id": str("Board id")}),
		run: func(c *client, a map[string]any) (json.RawMessage, error) {
			id, err := arg(a, "board_id")
			if err != nil {
				return nil, err
			}
			return c.call("GET", "/api/boards/"+url.PathEscape(id), nil)
		},
	},
	{
		Name:        "list_labels",
		Description: "List the allowed label colours.",
		InputSchema: obj(nil, map[string]any{}),
		run: func(c *client, _ map[string]any) (json.RawMessage, error) {
			return c.call("GET", "/api/labels", nil)
		},
	},
	{
		Name:        "create_card",
		Description: "Create a card at the end of a column. Title up to 500 characters, description up to 50000 (plain text).",
		InputSchema: obj([]string{"column_id", "title"}, map[string]any{
			"column_id":   str("Column id"),
			"title":       str("Card title"),
			"description": str("Optional plain-text description"),
			"label":       str("Optional label colour from list_labels, or empty"),
		}),
		run: func(c *client, a map[string]any) (json.RawMessage, error) {
			col, err := arg(a, "column_id")
			if err != nil {
				return nil, err
			}
			title, err := arg(a, "title")
			if err != nil {
				return nil, err
			}
			id, err := uuid.NewV7()
			if err != nil {
				return nil, err
			}
			desc, _ := a["description"].(string)
			label, _ := a["label"].(string)
			return c.call("POST", "/api/columns/"+url.PathEscape(col)+"/cards",
				map[string]string{"id": id.String(), "title": title, "description": desc, "label": label})
		},
	},
	{
		Name:        "update_card",
		Description: "Change a card's title, description or label. Only the fields given are changed.",
		InputSchema: obj([]string{"card_id"}, map[string]any{
			"card_id":     str("Card id"),
			"title":       str("New title"),
			"description": str("New description (replaces the old one)"),
			"label":       str("New label colour, or empty string to clear"),
		}),
		run: func(c *client, a map[string]any) (json.RawMessage, error) {
			id, err := arg(a, "card_id")
			if err != nil {
				return nil, err
			}
			patch := map[string]string{}
			for _, k := range []string{"title", "description", "label"} {
				if v, ok := a[k].(string); ok {
					patch[k] = v
				}
			}
			if len(patch) == 0 {
				return nil, errors.New("nothing to update")
			}
			return c.call("PATCH", "/api/cards/"+url.PathEscape(id), patch)
		},
	},
	{
		Name:        "move_card",
		Description: "Move a card to a column and index (0 = top). Use it to change a card's status.",
		InputSchema: obj([]string{"card_id", "to_column_id"}, map[string]any{
			"card_id":      str("Card id"),
			"to_column_id": str("Destination column id"),
			"to_index":     map[string]any{"type": "integer", "description": "Position in the destination column, default 0"},
		}),
		run: func(c *client, a map[string]any) (json.RawMessage, error) {
			id, err := arg(a, "card_id")
			if err != nil {
				return nil, err
			}
			to, err := arg(a, "to_column_id")
			if err != nil {
				return nil, err
			}
			idx := 0
			if f, ok := a["to_index"].(float64); ok {
				idx = int(f)
			}
			return c.call("PUT", "/api/cards/"+url.PathEscape(id)+"/move",
				map[string]any{"to_column_id": to, "to_index": idx})
		},
	},
	{
		Name:        "delete_card",
		Description: "Delete a card (soft delete on the server).",
		InputSchema: obj([]string{"card_id"}, map[string]any{"card_id": str("Card id")}),
		run: func(c *client, a map[string]any) (json.RawMessage, error) {
			id, err := arg(a, "card_id")
			if err != nil {
				return nil, err
			}
			return c.call("DELETE", "/api/cards/"+url.PathEscape(id), nil)
		},
	},
}

// --- JSON-RPC over stdio --------------------------------------------------

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func textResult(s string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": s}}, "isError": isErr}
}

func handle(c *client, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "scrumboard", "version": "1.0.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		for _, t := range tools {
			if t.Name == p.Name {
				out, err := t.run(c, p.Arguments)
				if err != nil {
					return textResult(err.Error(), true), nil
				}
				return textResult(string(out), false), nil
			}
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool " + p.Name}
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
}

func main() {
	log.SetOutput(os.Stderr) // stdout belongs to the protocol
	token := os.Getenv("SCRUMBOARD_TOKEN")
	if token == "" {
		log.Fatal("SCRUMBOARD_TOKEN is required")
	}
	base := strings.TrimRight(os.Getenv("SCRUMBOARD_URL"), "/")
	if base == "" {
		base = "http://localhost:8020"
	}
	c := &client{base: base, token: token, http: &http.Client{Timeout: 20 * time.Second}}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	enc := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			_ = enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: err.Error()}})
			continue
		}
		if len(req.ID) == 0 { // notification: no reply
			continue
		}
		res, rerr := handle(c, req)
		if err := enc.Encode(response{JSONRPC: "2.0", ID: req.ID, Result: res, Error: rerr}); err != nil {
			log.Fatal(err)
		}
	}
	if err := in.Err(); err != nil {
		log.Fatal(err)
	}
}
