package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

type BoardRepo struct{ d *DB }

var _ port.BoardRepository = (*BoardRepo)(nil)

const boardColumns = "id, name, created_at, updated_at, deleted_at"

func (r *BoardRepo) Create(ctx context.Context, b *domain.Board) error {
	_, err := r.d.q(ctx).ExecContext(ctx,
		`INSERT INTO boards (id, name, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?)`,
		b.ID, b.Name, formatTime(b.CreatedAt), formatTime(b.UpdatedAt), nullTime(b.DeletedAt),
	)
	if err != nil {
		return fmt.Errorf("insert board: %w", err)
	}
	return nil
}

func (r *BoardRepo) Get(ctx context.Context, id string) (*domain.Board, error) {
	return r.get(ctx, id, true)
}

func (r *BoardRepo) GetAny(ctx context.Context, id string) (*domain.Board, error) {
	return r.get(ctx, id, false)
}

func (r *BoardRepo) get(ctx context.Context, id string, liveOnly bool) (*domain.Board, error) {
	query := `SELECT ` + boardColumns + ` FROM boards WHERE id = ?`
	if liveOnly {
		query += ` AND deleted_at IS NULL`
	}
	b, err := scanBoard(r.d.q(ctx).QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return b, err
}

func (r *BoardRepo) List(ctx context.Context) ([]domain.Board, error) {
	rows, err := r.d.q(ctx).QueryContext(ctx,
		`SELECT `+boardColumns+` FROM boards WHERE deleted_at IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list boards: %w", err)
	}
	defer rows.Close()

	// Not a nil slice: a nil slice serialises to null and breaks the frontend
	// (02-testing.md §4.2).
	out := []domain.Board{}
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// GetWithChildren paints the screen: one board, its live columns, and their
// live cards, all in position order. Three queries rather than a join, because
// assembling a nested tree out of a flat result set is where the subtle
// ordering bugs live and this costs nothing at one user's data volume.
func (r *BoardRepo) GetWithChildren(ctx context.Context, id string) (*domain.Board, error) {
	board, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	cols, err := r.d.Columns().ListByBoard(ctx, id)
	if err != nil {
		return nil, err
	}

	rows, err := r.d.q(ctx).QueryContext(ctx,
		`SELECT `+cardColumns+`
		 FROM cards
		 WHERE deleted_at IS NULL
		   AND column_id IN (SELECT id FROM columns WHERE board_id = ? AND deleted_at IS NULL)
		 ORDER BY column_id, position`, id)
	if err != nil {
		return nil, fmt.Errorf("list cards of board: %w", err)
	}
	defer rows.Close()

	byColumn := map[string][]domain.Card{}
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		byColumn[c.ColumnID] = append(byColumn[c.ColumnID], *c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range cols {
		cards := byColumn[cols[i].ID]
		if cards == nil {
			cards = []domain.Card{}
		}
		cols[i].Cards = cards
	}
	board.Columns = cols
	return board, nil
}

func (r *BoardRepo) Update(ctx context.Context, b *domain.Board) error {
	res, err := r.d.q(ctx).ExecContext(ctx,
		`UPDATE boards SET name = ?, updated_at = ?, deleted_at = ? WHERE id = ?`,
		b.Name, formatTime(b.UpdatedAt), nullTime(b.DeletedAt), b.ID,
	)
	if err != nil {
		return fmt.Errorf("update board: %w", err)
	}
	return mustAffectOne(res)
}

// SoftDelete stamps the board and cascades to its columns and their cards. It
// is three statements rather than an ON DELETE CASCADE because nothing is
// actually being deleted — the rows stay, and the cascade is a rule of ours,
// not of the database.
func (r *BoardRepo) SoftDelete(ctx context.Context, id string, at time.Time) error {
	ts := formatTime(at)
	q := r.d.q(ctx)

	if _, err := q.ExecContext(ctx,
		`UPDATE cards SET deleted_at = ?
		 WHERE deleted_at IS NULL
		   AND column_id IN (SELECT id FROM columns WHERE board_id = ?)`, ts, id); err != nil {
		return fmt.Errorf("cascade delete cards: %w", err)
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE columns SET deleted_at = ? WHERE board_id = ? AND deleted_at IS NULL`, ts, id); err != nil {
		return fmt.Errorf("cascade delete columns: %w", err)
	}
	res, err := q.ExecContext(ctx,
		`UPDATE boards SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, ts, id)
	if err != nil {
		return fmt.Errorf("delete board: %w", err)
	}
	return mustAffectOne(res)
}

type scanner interface{ Scan(dest ...any) error }

func scanBoard(s scanner) (*domain.Board, error) {
	var (
		b         domain.Board
		created   string
		updated   string
		deletedAt sql.NullString
	)
	if err := s.Scan(&b.ID, &b.Name, &created, &updated, &deletedAt); err != nil {
		return nil, err
	}

	var err error
	if b.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if b.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	if b.DeletedAt, err = scanNullTime(deletedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

// mustAffectOne turns "the UPDATE matched nothing" into ErrNotFound. Without
// it a delete of a nonexistent id would silently succeed.
func mustAffectOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
