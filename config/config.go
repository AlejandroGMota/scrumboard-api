// Package config reads the process environment once, at startup.
package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Config is everything the process needs to know about where it is running.
// There is no config file: a handful of environment variables are the whole
// surface, and the ones with no safe default refuse to start rather than guess.
type Config struct {
	// Port the HTTP server listens on inside the container. nginx on the host
	// maps 127.0.0.1:8020 to it (01-structure.md §0).
	Port string

	// DBPath is the SQLite file. In production it lives on a Docker volume;
	// losing it loses the board, which is why the backup exists.
	DBPath string

	// Token is layer 2 of the access control (00-scope.md §6.1). nginx basic
	// auth is layer 1 and lives outside this process entirely.
	Token string

	// CORSOrigin is the single origin allowed to call the API. Never "*":
	// four lines of test exist specifically to stop that being added during
	// debugging and left in (02-testing.md §5.4).
	CORSOrigin string

	// RequestTimeout bounds a single request. It is what turns a query that
	// somehow never returns into a 503 rather than a stuck process.
	RequestTimeout time.Duration

	// MCP is the remote connector for Claude, served at /api/mcp/{secret}.
	// It is off unless SCRUMBOARD_MCP_SECRET is set.
	MCP MCP
}

// MCP configures the remote connector.
type MCP struct {
	// Secret is the last segment of the connector's URL and its only
	// credential. Separate from Token, so rotating one leaves the other alone.
	Secret string

	// Boards are the board ids the connector may touch; nil means every
	// board. It is required whenever Secret is set: the URL ends up in a
	// third-party settings page, and what it opens should be a decision.
	Boards []string

	// DefaultBoard is where a card goes when Claude names no board.
	DefaultBoard string
}

// secretAlphabet keeps the secret a single, unescaped path segment.
const secretAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

// ParseBoards reads a board list as SCRUMBOARD_MCP_BOARDS spells it: board
// ids separated by commas, or "*" alone for every board, which is nil.
func ParseBoards(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return nil, nil
	}
	var ids []string
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if id == "*" {
			return nil, errors.New(`"*" means every board and cannot be combined with ids`)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New(`list at least one board id, or "*" for every board`)
	}
	return ids, nil
}

// Load reads the environment and validates it.
func Load() (Config, error) {
	c := Config{
		Port:           env("PORT", "8080"),
		DBPath:         env("DB_PATH", "/data/scrumboard.db"),
		Token:          os.Getenv("SCRUMBOARD_TOKEN"),
		CORSOrigin:     env("CORS_ORIGIN", "http://localhost:5173"),
		RequestTimeout: 15 * time.Second,
	}

	var problems []string
	if c.Token == "" {
		problems = append(problems, "SCRUMBOARD_TOKEN is required: the API is exposed to the internet and this is its second layer of access control")
	}
	if len(c.Token) < 16 && c.Token != "" {
		problems = append(problems, "SCRUMBOARD_TOKEN must be at least 16 characters")
	}
	if c.CORSOrigin == "*" {
		problems = append(problems, `CORS_ORIGIN must name one origin, never "*"`)
	}
	problems = append(problems, c.loadMCP()...)
	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return c, nil
}

// loadMCP fills c.MCP and returns what is wrong with it. Without a secret the
// connector is off and the other two variables are not read.
func (c *Config) loadMCP() []string {
	secret := os.Getenv("SCRUMBOARD_MCP_SECRET")
	if secret == "" {
		return nil
	}

	var problems []string
	if len(secret) < 32 {
		problems = append(problems, "SCRUMBOARD_MCP_SECRET must be at least 32 characters (openssl rand -hex 32)")
	}
	if strings.Trim(secret, secretAlphabet) != "" {
		problems = append(problems, "SCRUMBOARD_MCP_SECRET may only hold letters, digits, - and _: it is a URL path segment (openssl rand -hex 32)")
	}
	if secret == c.Token {
		problems = append(problems, "SCRUMBOARD_MCP_SECRET must differ from SCRUMBOARD_TOKEN")
	}

	boards, err := ParseBoards(os.Getenv("SCRUMBOARD_MCP_BOARDS"))
	if err != nil {
		problems = append(problems, "SCRUMBOARD_MCP_BOARDS is required with SCRUMBOARD_MCP_SECRET: "+err.Error())
	}

	def := strings.TrimSpace(os.Getenv("SCRUMBOARD_MCP_DEFAULT_BOARD"))
	if def != "" && err == nil && boards != nil && !slices.Contains(boards, def) {
		problems = append(problems, "SCRUMBOARD_MCP_DEFAULT_BOARD must be one of SCRUMBOARD_MCP_BOARDS")
	}

	c.MCP = MCP{Secret: secret, Boards: boards, DefaultBoard: def}
	return problems
}

// ErrMissingToken is what Load reports when the token is absent, kept as a
// sentinel so main can print something more useful than a stack trace.
var ErrMissingToken = errors.New("SCRUMBOARD_TOKEN is not set")

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
