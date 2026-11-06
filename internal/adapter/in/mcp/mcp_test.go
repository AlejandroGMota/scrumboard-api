package mcp_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/alejandrogmota/scrumboard-api/internal/testutil"
)

const endpoint = "/api/mcp/" + testutil.MCPSecret

// rpc posts one raw JSON-RPC message the way claude.ai does: no token, only
// the secret in the path.
func rpc(env *testutil.Env, body string) *testutil.Response {
	return env.Client.NoAuth().POST(endpoint, body)
}

// call runs a tool and returns its text and whether it reported an error.
func call(t *testing.T, env *testutil.Env, name string, args map[string]any) (string, bool) {
	t.Helper()

	msg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("encoding the call: %v", err)
	}
	var res struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	rpc(env, string(msg)).ExpectStatus(http.StatusOK).JSON(&res)
	if res.Error != nil {
		t.Fatalf("%s: protocol error %s", name, res.Error.Message)
	}
	if len(res.Result.Content) != 1 {
		t.Fatalf("%s: want one content block, got %d", name, len(res.Result.Content))
	}
	return res.Result.Content[0].Text, res.Result.IsError
}

func mustCall(t *testing.T, env *testutil.Env, name string, args map[string]any) string {
	t.Helper()

	text, isErr := call(t, env, name, args)
	if isErr {
		t.Fatalf("%s returned an error: %s", name, text)
	}
	return text
}

// seedBoard creates a board with a chosen id, so a scope can name it before
// it exists, and returns its column ids in order.
func seedBoard(t *testing.T, env *testutil.Env, id, name string, columns ...string) []string {
	t.Helper()

	env.Client.POST("/api/boards", map[string]any{"id": id, "name": name}).ExpectStatus(http.StatusCreated)
	var ids []string
	for _, c := range columns {
		ids = append(ids, env.SeedColumn(t, id, c, nil))
	}
	return ids
}

func toolNames(t *testing.T, env *testutil.Env) []string {
	t.Helper()

	var res struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	rpc(env, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`).ExpectStatus(http.StatusOK).JSON(&res)
	var names []string
	for _, tool := range res.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestTheSecretIsTheOnlyKey(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	t.Run("the right secret needs no token", func(t *testing.T) {
		t.Parallel()
		rpc(env, ping).ExpectStatus(http.StatusOK)
	})

	t.Run("a wrong secret is a 404, as if nothing were there", func(t *testing.T) {
		t.Parallel()
		env.Client.NoAuth().POST("/api/mcp/"+strings.ToUpper(testutil.MCPSecret), ping).ExpectStatus(http.StatusNotFound)
	})

	t.Run("the API token is not a secret", func(t *testing.T) {
		t.Parallel()
		env.Client.POST("/api/mcp/"+testutil.Token, ping).ExpectStatus(http.StatusNotFound)
	})

	t.Run("only POST is served", func(t *testing.T) {
		t.Parallel()
		env.Client.NoAuth().GET(endpoint).ExpectStatus(http.StatusMethodNotAllowed)
	})
}

func TestProtocol(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)

	t.Run("initialize answers the version it was asked for, with instructions", func(t *testing.T) {
		t.Parallel()

		var res struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
				Instructions    string `json:"instructions"`
			} `json:"result"`
		}
		rpc(env, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`).
			ExpectStatus(http.StatusOK).JSON(&res)
		if res.Result.ProtocolVersion != "2025-06-18" {
			t.Errorf("protocolVersion = %q, want 2025-06-18", res.Result.ProtocolVersion)
		}
		if res.Result.Instructions == "" {
			t.Error("instructions are empty")
		}
	})

	t.Run("an unknown version gets the newest", func(t *testing.T) {
		t.Parallel()

		var res struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		rpc(env, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`).
			ExpectStatus(http.StatusOK).JSON(&res)
		if res.Result.ProtocolVersion != "2025-11-25" {
			t.Errorf("protocolVersion = %q, want 2025-11-25", res.Result.ProtocolVersion)
		}
	})

	t.Run("a notification is accepted with no body", func(t *testing.T) {
		t.Parallel()

		res := rpc(env, `{"jsonrpc":"2.0","method":"notifications/initialized"}`).ExpectStatus(http.StatusAccepted)
		if len(res.Body) != 0 {
			t.Errorf("body = %q, want empty", res.Body)
		}
	})

	t.Run("malformed JSON is a parse error", func(t *testing.T) {
		t.Parallel()

		var res struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		rpc(env, `{not json`).ExpectStatus(http.StatusOK).JSON(&res)
		if res.Error.Code != -32700 {
			t.Errorf("code = %d, want -32700", res.Error.Code)
		}
	})
}

func TestAddCardByName(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	cols := seedBoard(t, env, "b-day", "Día a día", "Hoy", "Próximos días", "Hecho")
	seedBoard(t, env, "b-work", "Work", "Todo")

	t.Run("names match regardless of case and accents", func(t *testing.T) {
		t.Parallel()

		out := mustCall(t, env, "add_card", map[string]any{"title": "Lavar trastes", "board": "dia a dia", "column": "proximos dias"})
		var res struct {
			Board  string `json:"board"`
			Column string `json:"column"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("decoding %s: %v", out, err)
		}
		if res.Board != "Día a día" || res.Column != "Próximos días" {
			t.Errorf("landed in %q › %q", res.Board, res.Column)
		}
		if got := env.CardTitlesIn(t, cols[1]); !slices.Contains(got, "Lavar trastes") {
			t.Errorf("Próximos días holds %v", got)
		}
	})

	t.Run("without a column it goes to the first one", func(t *testing.T) {
		t.Parallel()

		mustCall(t, env, "add_card", map[string]any{"title": "Barrer", "board": "DÍA A DÍA"})
		if got := env.CardTitlesIn(t, cols[0]); !slices.Contains(got, "Barrer") {
			t.Errorf("Hoy holds %v", got)
		}
	})

	t.Run("an unknown board lists the ones there are", func(t *testing.T) {
		t.Parallel()

		text, isErr := call(t, env, "add_card", map[string]any{"title": "x", "board": "Casa"})
		if !isErr || !strings.Contains(text, `"Work"`) {
			t.Errorf("isError = %v, text = %s", isErr, text)
		}
	})

	t.Run("two boards and no default: Claude has to name one", func(t *testing.T) {
		t.Parallel()

		if text, isErr := call(t, env, "add_card", map[string]any{"title": "x"}); !isErr {
			t.Errorf("want an error, got %s", text)
		}
	})

	t.Run("it goes through the API: the change is published", func(t *testing.T) {
		t.Parallel()

		mustCall(t, env, "add_card", map[string]any{"title": "Publicado", "board": "work"})
		found := false
		for _, e := range env.Events.OfType(domain.EventCardCreated) {
			if e.BoardID == "b-work" {
				found = true
			}
		}
		if !found {
			t.Error("no card.created event for the Work board")
		}
	})
}

func TestScope(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t, testutil.WithMCPScope([]string{"b-day"}, "b-day"))
	day := seedBoard(t, env, "b-day", "Día a día", "Hoy", "Hecho")
	work := seedBoard(t, env, "b-work", "Work", "Todo")
	secretCard := env.SeedCard(t, work[0], "Client notes")
	dayCard := env.SeedCard(t, day[0], "Tender los manteles")

	t.Run("list_boards shows only the listed boards", func(t *testing.T) {
		t.Parallel()

		out := mustCall(t, env, "list_boards", nil)
		if !strings.Contains(out, "b-day") || strings.Contains(out, "b-work") {
			t.Errorf("list_boards = %s", out)
		}
	})

	t.Run("add_card without a board uses the default", func(t *testing.T) {
		t.Parallel()

		mustCall(t, env, "add_card", map[string]any{"title": "Cambiar billetes"})
		if got := env.CardTitlesIn(t, day[0]); !slices.Contains(got, "Cambiar billetes") {
			t.Errorf("Hoy holds %v", got)
		}
	})

	t.Run("a board outside the list cannot be named", func(t *testing.T) {
		t.Parallel()

		if text, isErr := call(t, env, "add_card", map[string]any{"title": "x", "board": "Work"}); !isErr {
			t.Errorf("want an error, got %s", text)
		}
	})

	t.Run("nor reached by id", func(t *testing.T) {
		t.Parallel()

		for name, args := range map[string]map[string]any{
			"get_board":   {"board_id": "b-work"},
			"create_card": {"column_id": work[0], "title": "x"},
			"update_card": {"card_id": secretCard, "title": "x"},
			"delete_card": {"card_id": secretCard},
			"move_card":   {"card_id": secretCard, "to_column_id": day[0]},
		} {
			if text, isErr := call(t, env, name, args); !isErr {
				t.Errorf("%s: want an error, got %s", name, text)
			}
		}
		if got := env.CardTitlesIn(t, work[0]); !slices.Equal(got, []string{"Client notes"}) {
			t.Errorf("Work › Todo holds %v", got)
		}
	})

	t.Run("a card cannot be moved out of the list", func(t *testing.T) {
		t.Parallel()

		if text, isErr := call(t, env, "move_card", map[string]any{"card_id": dayCard, "to_column_id": work[0]}); !isErr {
			t.Errorf("want an error, got %s", text)
		}
	})

	t.Run("create_board is not offered", func(t *testing.T) {
		t.Parallel()

		if names := toolNames(t, env); slices.Contains(names, "create_board") {
			t.Errorf("tools = %v", names)
		}
	})
}

func TestClearColumn(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)
	cols := seedBoard(t, env, "b-day", "Día a día", "Hoy", "Hecho")
	env.SeedCards(t, cols[0], "Pendiente")
	env.SeedCards(t, cols[1], "Uno", "Dos", "Tres")

	out := mustCall(t, env, "clear_column", map[string]any{"board": "dia a dia", "column": "hecho"})
	if !strings.Contains(out, `"deleted":3`) {
		t.Errorf("clear_column = %s", out)
	}
	if got := env.CardTitlesIn(t, cols[1]); len(got) != 0 {
		t.Errorf("Hecho still holds %v", got)
	}
	if got := env.CardTitlesIn(t, cols[0]); !slices.Equal(got, []string{"Pendiente"}) {
		t.Errorf("Hoy holds %v, want it untouched", got)
	}
}

func TestCreateBoard(t *testing.T) {
	t.Parallel()

	env := testutil.NewEnv(t)

	out := mustCall(t, env, "create_board", map[string]any{"name": "Compras", "columns": []string{"Falta", "Comprado"}})
	var board struct {
		Name    string `json:"name"`
		Columns []struct {
			Name string `json:"name"`
		} `json:"columns"`
	}
	if err := json.Unmarshal([]byte(out), &board); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	if board.Name != "Compras" || len(board.Columns) != 2 || board.Columns[0].Name != "Falta" || board.Columns[1].Name != "Comprado" {
		t.Errorf("create_board = %s", out)
	}
}
