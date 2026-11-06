// Command mcp is a Model Context Protocol server over stdio that lets Claude
// read and edit the board through the public HTTP API. The tools live in
// internal/adapter/in/mcp, shared with the remote connector the API serves.
//
// Configuration:
//
//	SCRUMBOARD_URL                base URL, e.g. https://scrumboard.alejandrogmota.com (default http://localhost:8020)
//	SCRUMBOARD_TOKEN              the same token the API checks in X-Scrumboard-Token
//	SCRUMBOARD_MCP_BOARDS         optional: board ids, comma-separated, to limit the tools to (default every board)
//	SCRUMBOARD_MCP_DEFAULT_BOARD  optional: the board id add_card uses when no board is named
package main

import (
	"bufio"
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alejandrogmota/scrumboard-api/config"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/in/mcp"
)

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
	var boards []string
	if raw := os.Getenv("SCRUMBOARD_MCP_BOARDS"); raw != "" {
		var err error
		if boards, err = config.ParseBoards(raw); err != nil {
			log.Fatalf("SCRUMBOARD_MCP_BOARDS: %v", err)
		}
	}

	srv := mcp.NewServer(mcp.Options{
		Caller:       &mcp.HTTPCaller{Base: base, Token: token, Client: &http.Client{Timeout: 20 * time.Second}},
		Boards:       boards,
		DefaultBoard: strings.TrimSpace(os.Getenv("SCRUMBOARD_MCP_DEFAULT_BOARD")),
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	out := bufio.NewWriter(os.Stdout)
	ctx := context.Background()
	for in.Scan() {
		res := srv.Respond(ctx, in.Bytes())
		if res == nil { // a notification: no reply
			continue
		}
		out.Write(res)
		out.WriteByte('\n')
		if err := out.Flush(); err != nil {
			log.Fatal(err)
		}
	}
	if err := in.Err(); err != nil {
		log.Fatal(err)
	}
}
