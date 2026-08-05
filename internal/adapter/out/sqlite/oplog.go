package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

type OpLogRepo struct{ d *DB }

var _ port.OpLogRepository = (*OpLogRepo)(nil)

func (r *OpLogRepo) Applied(ctx context.Context, opID string) (bool, error) {
	var at string
	err := r.d.q(ctx).QueryRowContext(ctx,
		`SELECT applied_at FROM op_log WHERE op_id = ?`, opID).Scan(&at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("read op log: %w", err)
	default:
		return true, nil
	}
}

// Record marks an operation as applied. INSERT OR IGNORE rather than a plain
// INSERT: two devices replaying the same queue is exactly the situation this
// table exists for, and it should not produce an error.
func (r *OpLogRepo) Record(ctx context.Context, opID string, at time.Time) error {
	_, err := r.d.q(ctx).ExecContext(ctx,
		`INSERT OR IGNORE INTO op_log (op_id, applied_at) VALUES (?, ?)`, opID, formatTime(at))
	if err != nil {
		return fmt.Errorf("write op log: %w", err)
	}
	return nil
}
