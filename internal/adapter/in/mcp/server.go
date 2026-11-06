// Package mcp is a Model Context Protocol server that lets Claude read and
// edit the board. It holds no state of its own: every tool call is one or more
// requests to the HTTP API, through a Caller.
//
// It has two transports. cmd/mcp speaks it over stdio, for Claude Code on a
// laptop. NewHandler speaks it over HTTP, for claude.ai and its phone app,
// which can only reach a server by URL.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
)

// protocolVersions are the revisions this server answers to, newest first.
// Nothing here depends on what changed between them: tools are all it offers.
var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const instructions = "The user's Kanban boards. A to-do the user dictates without naming a board " +
	"goes to add_card with no board or column: it lands at the end of the default board's first " +
	"column. Name a board or column only when the user does; names match regardless of case and " +
	"accents. To find one card, use search_cards rather than reading a whole board. clear_column " +
	"deletes every card in a column: first say which column and how many cards it holds, and call " +
	"it only after the user confirms."

// Options configure a Server.
type Options struct {
	Caller Caller

	// Boards limits every tool to these board ids. Nil means every board.
	// A board outside the list answers exactly like one that does not exist.
	Boards []string

	// DefaultBoard is the board add_card uses when Claude names none.
	DefaultBoard string

	Logger *slog.Logger
}

// Server answers JSON-RPC messages. It is safe for concurrent use.
type Server struct {
	caller       Caller
	boards       []string
	defaultBoard string
	logger       *slog.Logger
	tools        []tool
}

func NewServer(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	s := &Server{caller: o.Caller, boards: o.Boards, defaultBoard: o.DefaultBoard, logger: o.Logger}
	for _, t := range allTools {
		// Creating a board is only offered with access to every board: a
		// new board would be outside a fixed list the moment it existed.
		if t.unrestrictedOnly && s.restricted() {
			continue
		}
		s.tools = append(s.tools, t)
	}
	return s
}

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

// Respond takes one raw message, or a batch, and returns the encoded answer.
// It returns nil when nothing is owed: a notification, or a batch of them.
func (s *Server) Respond(ctx context.Context, raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(raw, &batch); err != nil {
			return encode(parseError(err))
		}
		var out []response
		for _, m := range batch {
			if r := s.one(ctx, m); r != nil {
				out = append(out, *r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return encode(out)
	}
	if r := s.one(ctx, raw); r != nil {
		return encode(r)
	}
	return nil
}

func (s *Server) one(ctx context.Context, raw json.RawMessage) *response {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		r := parseError(err)
		return &r
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return &response{JSONRPC: "2.0", ID: idOrNull(req.ID), Error: &rpcError{Code: -32600, Message: "invalid request"}}
	}
	if len(req.ID) == 0 { // a notification: no reply
		return nil
	}
	res, rerr := s.handle(ctx, req)
	return &response{JSONRPC: "2.0", ID: req.ID, Result: res, Error: rerr}
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p) // an absent version gets the newest
		version := protocolVersions[0]
		if slices.Contains(protocolVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "scrumboard", "version": "1.2.0"},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		for _, t := range s.tools {
			if t.Name != p.Name {
				continue
			}
			out, err := t.run(ctx, s, args(p.Arguments))
			if err != nil {
				s.logger.Warn("mcp tool failed", slog.String("tool", t.Name), slog.String("error", err.Error()))
				return textResult(err.Error(), true), nil
			}
			s.logger.Info("mcp tool", slog.String("tool", t.Name))
			return textResult(string(out), false), nil
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool " + p.Name}
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
}

func textResult(s string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": s}}, "isError": isErr}
}

func parseError(err error) response {
	return response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: err.Error()}}
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Every value here is built from strings, maps and RawMessages that
		// already decoded once, so this is a bug rather than bad input.
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error"}}`)
	}
	return b
}
