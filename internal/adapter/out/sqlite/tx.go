package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

// querier is what every repository method actually talks to: either the pool
// or the transaction currently in the context. Repositories never choose.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type txKey struct{}

// WithTx runs fn inside a transaction, putting it in the context so the
// repositories fn calls join it without being told.
//
// A transaction already in flight is reused rather than nested. That is not a
// convenience: the pool holds exactly one connection, so a second Begin while
// the first transaction is open would wait for a connection that only the
// caller can release — a deadlock, not an error. The sync service leans on
// this, applying each operation through services that open transactions of
// their own.
func (d *DB) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return fn(ctx)
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
			return fmt.Errorf("%w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// q returns the transaction in the context, or the pool when there is none.
func (d *DB) q(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}
	return d.db
}

var _ port.TxManager = (*DB)(nil)
