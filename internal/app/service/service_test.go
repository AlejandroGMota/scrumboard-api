package service_test

import (
	"context"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/app/port/fake"
	"github.com/alejandrogmota/scrumboard-api/internal/app/service"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// rig is everything a service test needs, wired over the in-memory fakes.
// These tests are about orchestration — what is called, in what order, what is
// published — so a real repository would only add noise (02-testing.md §2.1).
type rig struct {
	Boards  *service.Board
	Columns *service.Column
	Cards   *service.Card
	Sync    *service.Sync

	Store  *fake.Store
	Events *fake.Publisher
	Clock  *fake.Clock
	IDs    *fake.IDGen
}

func newRig(t *testing.T) *rig {
	t.Helper()

	store := fake.NewStore()
	events := fake.NewPublisher()
	clock := fake.NewClock()
	ids := fake.NewIDGen()

	d := service.Deps{
		Boards:  store.Boards(),
		Columns: store.Columns(),
		Cards:   store.Cards(),
		OpLog:   store.OpLog(),
		Tx:      store,
		Events:  events,
		Clock:   clock,
		IDs:     ids,
	}
	return &rig{
		Boards:  service.NewBoard(d),
		Columns: service.NewColumn(d),
		Cards:   service.NewCard(d),
		Sync:    service.NewSync(d),
		Store:   store,
		Events:  events,
		Clock:   clock,
		IDs:     ids,
	}
}

// board creates a board and fails the test if it cannot.
func (r *rig) board(t *testing.T, name string) *domain.Board {
	t.Helper()

	b, _, err := r.Boards.Create(context.Background(), port.CreateBoardCmd{Name: name})
	if err != nil {
		t.Fatalf("creating board %q: %v", name, err)
	}
	return b
}

func (r *rig) column(t *testing.T, boardID, name string, wip *int) *domain.Column {
	t.Helper()

	c, _, err := r.Columns.Create(context.Background(), port.CreateColumnCmd{
		BoardID: boardID, Name: name, WIPLimit: wip,
	})
	if err != nil {
		t.Fatalf("creating column %q: %v", name, err)
	}
	return c
}

func (r *rig) card(t *testing.T, columnID, title string) *domain.Card {
	t.Helper()

	c, _, err := r.Cards.Create(context.Background(), port.CreateCardCmd{ColumnID: columnID, Title: title})
	if err != nil {
		t.Fatalf("creating card %q: %v", title, err)
	}
	return c
}

// titlesIn returns the titles of a column's live cards in position order, which
// is what almost every move assertion is really about.
func (r *rig) titlesIn(t *testing.T, columnID string) []string {
	t.Helper()

	cards, err := r.Store.Cards().ListByColumn(context.Background(), columnID)
	if err != nil {
		t.Fatalf("listing cards: %v", err)
	}
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Title
	}
	return out
}

// assertPositionsCompact pins the invariant that survives every move: 0..n-1,
// no gaps, no ties (02-testing.md §4.4).
func (r *rig) assertPositionsCompact(t *testing.T, columnID string) {
	t.Helper()

	cards, err := r.Store.Cards().ListByColumn(context.Background(), columnID)
	if err != nil {
		t.Fatalf("listing cards: %v", err)
	}
	for i, c := range cards {
		if c.Position != i {
			t.Errorf("column %s: card %q has position %d, want %d (positions must be 0..n-1)", columnID, c.Title, c.Position, i)
		}
	}
}

func ptr[T any](v T) *T { return &v }
