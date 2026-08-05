package sqlite_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// "database is locked" is the failure everyone meets with SQLite, and the
// combination that avoids it — WAL, a busy timeout and a pool of exactly one
// connection (00-scope.md §6.3) — is worth pinning rather than trusting.
//
// Run with -race. A pool that can hand out two connections is a pool that can
// produce a conflict, and this is the test that would notice.
func TestConcurrentWritersNeverSeeALockedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)

	const writers, perWriter = 8, 10

	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				id := fmt.Sprintf("k-%d-%d", w, i)
				c, err := domain.NewCard(id, col.ID, "card "+id, "", "", 0, t0)
				if err != nil {
					errs <- err
					return
				}
				if err := db.Cards().Create(ctx, c); err != nil {
					errs <- fmt.Errorf("writer %d, card %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	cards, err := db.Cards().ListByColumn(ctx, col.ID)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(cards) != writers*perWriter {
		t.Errorf("%d cards stored, want %d: some writes were lost", len(cards), writers*perWriter)
	}
}

// The same under transactions, which is where a second connection would
// actually collide.
func TestConcurrentTransactionsSerialise(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	_, col := seed(t, db, nil)

	const writers = 8

	var wg sync.WaitGroup
	errs := make(chan error, writers)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			err := db.WithTx(ctx, func(ctx context.Context) error {
				// Read then write inside the transaction: the pattern every
				// reorder uses, and the one that would deadlock if the read
				// went to the pool instead of the transaction.
				existing, err := db.Cards().ListByColumn(ctx, col.ID)
				if err != nil {
					return err
				}
				c, err := domain.NewCard(fmt.Sprintf("k-%d", w), col.ID, "card", "", "", len(existing), t0)
				if err != nil {
					return err
				}
				return db.Cards().Create(ctx, c)
			})
			if err != nil {
				errs <- fmt.Errorf("writer %d: %w", w, err)
			}
		}(w)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent transaction failed: %v", err)
	}

	cards, err := db.Cards().ListByColumn(ctx, col.ID)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(cards) != writers {
		t.Errorf("%d cards stored, want %d", len(cards), writers)
	}
}
