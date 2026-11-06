package config

import (
	"slices"
	"strings"
	"testing"
)

const goodSecret = "0123456789abcdef0123456789abcdef" // gitleaks:allow (a test value)

func TestMCPConfig(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		problem string // empty: it loads
		boards  []string
	}{
		{name: "off without a secret", env: map[string]string{"SCRUMBOARD_MCP_BOARDS": "a"}},
		{name: "a list of boards", env: map[string]string{"SCRUMBOARD_MCP_SECRET": goodSecret, "SCRUMBOARD_MCP_BOARDS": " a, b ", "SCRUMBOARD_MCP_DEFAULT_BOARD": "b"}, boards: []string{"a", "b"}},
		{name: "every board, said out loud", env: map[string]string{"SCRUMBOARD_MCP_SECRET": goodSecret, "SCRUMBOARD_MCP_BOARDS": "*"}},
		{name: "a secret without boards", env: map[string]string{"SCRUMBOARD_MCP_SECRET": goodSecret}, problem: "SCRUMBOARD_MCP_BOARDS is required"},
		{name: "a short secret", env: map[string]string{"SCRUMBOARD_MCP_SECRET": "short", "SCRUMBOARD_MCP_BOARDS": "*"}, problem: "at least 32"},
		{name: "a secret that is not one path segment", env: map[string]string{"SCRUMBOARD_MCP_SECRET": goodSecret + "/x+y=", "SCRUMBOARD_MCP_BOARDS": "*"}, problem: "URL path segment"},
		{name: "the token reused", env: map[string]string{"SCRUMBOARD_MCP_SECRET": "token-" + goodSecret, "SCRUMBOARD_MCP_BOARDS": "*"}, problem: "differ from SCRUMBOARD_TOKEN"},
		{name: "a default outside the list", env: map[string]string{"SCRUMBOARD_MCP_SECRET": goodSecret, "SCRUMBOARD_MCP_BOARDS": "a", "SCRUMBOARD_MCP_DEFAULT_BOARD": "b"}, problem: "must be one of"},
		{name: "every board mixed with ids", env: map[string]string{"SCRUMBOARD_MCP_SECRET": goodSecret, "SCRUMBOARD_MCP_BOARDS": "a,*"}, problem: "cannot be combined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SCRUMBOARD_TOKEN", "token-"+goodSecret)
			for _, k := range []string{"SCRUMBOARD_MCP_SECRET", "SCRUMBOARD_MCP_BOARDS", "SCRUMBOARD_MCP_DEFAULT_BOARD"} {
				t.Setenv(k, tc.env[k])
			}

			c, err := Load()
			if tc.problem != "" {
				if err == nil || !strings.Contains(err.Error(), tc.problem) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.problem)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !slices.Equal(c.MCP.Boards, tc.boards) {
				t.Errorf("Boards = %v, want %v", c.MCP.Boards, tc.boards)
			}
		})
	}
}
