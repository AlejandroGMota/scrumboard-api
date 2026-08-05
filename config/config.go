// Package config reads the process environment once, at startup.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is everything the process needs to know about where it is running.
// There is no config file: four environment variables are the whole surface,
// and the ones with no safe default refuse to start rather than guess.
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
	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return c, nil
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
