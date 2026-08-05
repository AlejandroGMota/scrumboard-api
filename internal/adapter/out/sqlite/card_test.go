package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/sqlite"
	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
	"github.com/google/go-cmp/cmp"
)

var t0 = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// newDB opens a database on a temporary file, migrated and empty. Every test
// gets its own: no shared state, no ordering requirements.
func newDB(t *testing.T) *sqlite.DB {
	t.Helper()

	db, err := sqlite.Open(context.Background(), t.TempDir()+"/scrumboard.db")
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seed creates a board with one column and returns both.
func seed(t *testing.T, db *sqlite.DB, wip *int) (*domain.Board, *domain.Column) {
	t.Helper()
	ctx := context.Background()

	b, err := domain.NewBoard("b1", "Work", t0)
	if err != nil {
		t.Fatalf("building the board: %v", err)
	}
	if err := db.Boards().Create(ctx, b); err != nil {
		t.Fatalf("inserting the board: %v", err)
	}
	c, err := domain.NewColumn("c1", b.ID, "Todo", wip, 0, t0)
	if err != nil {
		t.Fatalf("building the column: %v", err)
	}
	if err := db.Columns().Create(ctx, c); err != nil {
		t.Fatalf("inserting the column: %v", err)
	}
	return b, c
}

// insertCard takes a context rather than making one, because inside a
// transaction the context IS the transaction. A helper that quietly used
// context.Background() would ask the pool for a second connection while the
// caller holds the only one — and wait forever.
func insertCard(t *testing.T, ctx context.Context, db *sqlite.DB, columnID, id, title string, pos int) *domain.Card {
	t.Helper()

	c, err := domain.NewCard(id, columnID, title, "", "", pos, t0)
	if err != nil {
		t.Fatalf("building the card: %v", err)
	}
	if err := db.Cards().Create(ctx, c); err != nil {
		t.Fatalf("inserting the card: %v", err)
	}
	return c
}

// The point of testing this layer at all: that the SQL does what it claims.
func TestCardRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)

	want, err := domain.NewCard("k1", col.ID, "Write the tests", "before the handler", "blue", 0, t0)
	if err != nil {
		t.Fatalf("building the card: %v", err)
	}
	if err := db.Cards().Create(ctx, want); err != nil {
		t.Fatalf("inserting: %v", err)
	}

	got, err := db.Cards().Get(ctx, "k1")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestCardTimestampsSurviveTheRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)

	// A time with a non-zero nanosecond component and a non-UTC location:
	// both are ways a text timestamp column loses information quietly.
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skipf("no timezone database available: %v", err)
	}
	at := time.Date(2026, 8, 4, 14, 30, 15, 123456789, madrid)

	c, err := domain.NewCard("k1", col.ID, "Card", "", "", 0, at)
	if err != nil {
		t.Fatalf("building the card: %v", err)
	}
	if err := db.Cards().Create(ctx, c); err != nil {
		t.Fatalf("inserting: %v", err)
	}

	got, err := db.Cards().Get(ctx, "k1")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !got.CreatedAt.Equal(at) {
		t.Errorf("CreatedAt = %v, want the same instant as %v", got.CreatedAt, at)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("Location = %v, want UTC: everything stored is UTC", got.CreatedAt.Location())
	}
}

func TestCardSoftDeleteHidesFromLiveReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)
	insertCard(t, ctx, db, col.ID, "k1", "gone", 0)

	deletedAt := t0.Add(time.Minute)
	if err := db.Cards().SoftDelete(ctx, "k1", deletedAt); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	if _, err := db.Cards().Get(ctx, "k1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get on a deleted card = %v, want %v", err, domain.ErrNotFound)
	}
	dead, err := db.Cards().GetAny(ctx, "k1")
	if err != nil {
		t.Fatalf("GetAny must still find it: %v", err)
	}
	if dead.DeletedAt == nil || !dead.DeletedAt.Equal(deletedAt) {
		t.Errorf("DeletedAt = %v, want %v", dead.DeletedAt, deletedAt)
	}
	if n, _ := db.Cards().CountByColumn(ctx, col.ID); n != 0 {
		t.Errorf("CountByColumn = %d, want 0: a deleted card must not hold a WIP slot", n)
	}
	if cards, _ := db.Cards().ListByColumn(ctx, col.ID); len(cards) != 0 {
		t.Errorf("%d live cards, want 0", len(cards))
	}
}

func TestCardSoftDeleteIsIdempotentAtTheSQLLevel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)
	insertCard(t, ctx, db, col.ID, "k1", "gone", 0)

	if err := db.Cards().SoftDelete(ctx, "k1", t0); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// The second UPDATE matches nothing, and the repository reports that
	// honestly. The service is what decides a repeat delete is a 204.
	if err := db.Cards().SoftDelete(ctx, "k1", t0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete = %v, want %v", err, domain.ErrNotFound)
	}
}

func TestSetPositionsDoesNotTouchUpdatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)
	insertCard(t, ctx, db, col.ID, "k1", "a", 0)
	insertCard(t, ctx, db, col.ID, "k2", "b", 1)

	err := db.Cards().SetPositions(ctx, []port.Position{
		{ID: "k2", Position: 0},
		{ID: "k1", Position: 1},
	})
	if err != nil {
		t.Fatalf("setting positions: %v", err)
	}

	cards, err := db.Cards().ListByColumn(ctx, col.ID)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if cards[0].ID != "k2" || cards[1].ID != "k1" {
		t.Fatalf("order = %s, %s; want k2, k1", cards[0].ID, cards[1].ID)
	}
	for _, c := range cards {
		if !c.UpdatedAt.Equal(t0) {
			t.Errorf("card %s UpdatedAt = %v, want it untouched at %v", c.ID, c.UpdatedAt, t0)
		}
	}
}

func TestSetPositionsRejectsAnUnknownID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)
	insertCard(t, ctx, db, col.ID, "k1", "a", 0)

	err := db.Cards().SetPositions(ctx, []port.Position{{ID: "nope", Position: 0}})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want %v: a position write that matches nothing is a bug worth surfacing", err, domain.ErrNotFound)
	}
}

// Foreign keys are off by default in SQLite, which is a trap. The DSN turns
// them on; this proves it stayed on.
func TestForeignKeysAreEnforced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	c, err := domain.NewCard("k1", "no-such-column", "orphan", "", "", 0, t0)
	if err != nil {
		t.Fatalf("building the card: %v", err)
	}
	if err := db.Cards().Create(ctx, c); err == nil {
		t.Fatal("inserting a card into a nonexistent column succeeded; foreign keys are off")
	}
}

func TestGetWithChildrenNestsAndOrders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	board, todo := seed(t, db, nil)

	doing, err := domain.NewColumn("c2", board.ID, "Doing", nil, 1, t0)
	if err != nil {
		t.Fatalf("building the column: %v", err)
	}
	if err := db.Columns().Create(ctx, doing); err != nil {
		t.Fatalf("inserting the column: %v", err)
	}

	// Inserted out of order on purpose: the ordering has to come from the
	// query, not from the insertion sequence.
	insertCard(t, ctx, db, todo.ID, "k2", "second", 1)
	insertCard(t, ctx, db, todo.ID, "k1", "first", 0)
	insertCard(t, ctx, db, doing.ID, "k3", "elsewhere", 0)

	got, err := db.Boards().GetWithChildren(ctx, board.ID)
	if err != nil {
		t.Fatalf("reading the board: %v", err)
	}
	if len(got.Columns) != 2 {
		t.Fatalf("%d columns, want 2", len(got.Columns))
	}
	if got.Columns[0].Name != "Todo" || got.Columns[1].Name != "Doing" {
		t.Errorf("columns = %q, %q; want Todo, Doing", got.Columns[0].Name, got.Columns[1].Name)
	}
	titles := []string{got.Columns[0].Cards[0].Title, got.Columns[0].Cards[1].Title}
	if diff := cmp.Diff([]string{"first", "second"}, titles); diff != "" {
		t.Errorf("card order mismatch (-want +got):\n%s", diff)
	}
	if len(got.Columns[1].Cards) != 1 {
		t.Errorf("%d cards in Doing, want 1", len(got.Columns[1].Cards))
	}
}

func TestGetWithChildrenGivesEmptySlicesNotNil(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	board, _ := seed(t, db, nil)

	got, err := db.Boards().GetWithChildren(ctx, board.ID)
	if err != nil {
		t.Fatalf("reading the board: %v", err)
	}
	// A nil slice serialises to null and breaks the frontend.
	if got.Columns[0].Cards == nil {
		t.Error("Cards is nil; it must be an empty slice so it serialises to []")
	}
}

func TestBoardSoftDeleteCascades(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	board, col := seed(t, db, nil)
	insertCard(t, ctx, db, col.ID, "k1", "a", 0)

	if err := db.Boards().SoftDelete(ctx, board.ID, t0.Add(time.Minute)); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	if _, err := db.Boards().Get(ctx, board.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the board is still live: %v", err)
	}
	if _, err := db.Columns().Get(ctx, col.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the column is still live: %v", err)
	}
	if _, err := db.Cards().Get(ctx, "k1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the card is still live: %v", err)
	}
	// The rows are still there. That is the whole point of a soft delete.
	if _, err := db.Cards().GetAny(ctx, "k1"); err != nil {
		t.Errorf("the card row should not have been removed: %v", err)
	}
}

func TestListBoardsIsEmptySliceNotNil(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	boards, err := db.Boards().List(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if boards == nil {
		t.Fatal("List returned nil; it must return an empty slice so it serialises to []")
	}
	if len(boards) != 0 {
		t.Errorf("%d boards, want 0", len(boards))
	}
}

func TestTransactionRollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)
	wantErr := errors.New("something went wrong halfway")

	err := db.WithTx(ctx, func(ctx context.Context) error {
		insertCard(t, ctx, db, col.ID, "k1", "a", 0)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, err := db.Cards().GetAny(ctx, "k1"); !errors.Is(err, domain.ErrNotFound) {
		t.Error("the card survived a rolled-back transaction")
	}
}

// The pool holds one connection, so a nested Begin would wait for a connection
// only the caller can release. WithTx reuses instead, and the sync service
// depends on it.
func TestNestedTransactionsReuseTheOuterOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)

	done := make(chan error, 1)
	go func() {
		done <- db.WithTx(ctx, func(ctx context.Context) error {
			return db.WithTx(ctx, func(ctx context.Context) error {
				c, err := domain.NewCard("k1", col.ID, "nested", "", "", 0, t0)
				if err != nil {
					return err
				}
				return db.Cards().Create(ctx, c)
			})
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested transaction: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nested WithTx deadlocked: the inner call opened a second transaction")
	}

	if _, err := db.Cards().Get(ctx, "k1"); err != nil {
		t.Errorf("the nested write did not commit: %v", err)
	}
}
