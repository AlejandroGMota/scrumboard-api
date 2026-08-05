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

type ColumnRepo struct{ d *DB }

var _ port.ColumnRepository = (*ColumnRepo)(nil)

const columnFields = "id, board_id, name, position, wip_limit, created_at, updated_at, deleted_at"

func (r *ColumnRepo) Create(ctx context.Context, c *domain.Column) error {
	_, err := r.d.q(ctx).ExecContext(ctx,
		`INSERT INTO columns (id, board_id, name, position, wip_limit, created_at, updated_at, deleted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.BoardID, c.Name, c.Position, nullInt(c.WIPLimit),
		formatTime(c.CreatedAt), formatTime(c.UpdatedAt), nullTime(c.DeletedAt),
	)
	if err != nil {
		return fmt.Errorf("insert column: %w", err)
	}
	return nil
}

func (r *ColumnRepo) Get(ctx context.Context, id string) (*domain.Column, error) {
	return r.get(ctx, id, true)
}

func (r *ColumnRepo) GetAny(ctx context.Context, id string) (*domain.Column, error) {
	return r.get(ctx, id, false)
}

func (r *ColumnRepo) get(ctx context.Context, id string, liveOnly bool) (*domain.Column, error) {
	query := `SELECT ` + columnFields + ` FROM columns WHERE id = ?`
	if liveOnly {
		query += ` AND deleted_at IS NULL`
	}
	c, err := scanColumn(r.d.q(ctx).QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *ColumnRepo) ListByBoard(ctx context.Context, boardID string) ([]domain.Column, error) {
	rows, err := r.d.q(ctx).QueryContext(ctx,
		`SELECT `+columnFields+` FROM columns
		 WHERE board_id = ? AND deleted_at IS NULL
		 ORDER BY position, id`, boardID)
	if err != nil {
		return nil, fmt.Errorf("list columns: %w", err)
	}
	defer rows.Close()

	out := []domain.Column{}
	for rows.Next() {
		c, err := scanColumn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r *ColumnRepo) Update(ctx context.Context, c *domain.Column) error {
	res, err := r.d.q(ctx).ExecContext(ctx,
		`UPDATE columns SET name = ?, position = ?, wip_limit = ?, updated_at = ?, deleted_at = ?
		 WHERE id = ?`,
		c.Name, c.Position, nullInt(c.WIPLimit), formatTime(c.UpdatedAt), nullTime(c.DeletedAt), c.ID,
	)
	if err != nil {
		return fmt.Errorf("update column: %w", err)
	}
	return mustAffectOne(res)
}

// SetPositions rewrites a whole group's order. updated_at is left alone on
// purpose — see port.ColumnRepository.SetPositions for why.
func (r *ColumnRepo) SetPositions(ctx context.Context, ps []port.Position) error {
	return setPositions(ctx, r.d, "columns", ps)
}

func (r *ColumnRepo) SoftDelete(ctx context.Context, id string, at time.Time) error {
	res, err := r.d.q(ctx).ExecContext(ctx,
		`UPDATE columns SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("delete column: %w", err)
	}
	return mustAffectOne(res)
}

func scanColumn(s scanner) (*domain.Column, error) {
	var (
		c        domain.Column
		wipLimit sql.NullInt64
		created  string
		updated  string
		deleted  sql.NullString
	)
	if err := s.Scan(&c.ID, &c.BoardID, &c.Name, &c.Position, &wipLimit, &created, &updated, &deleted); err != nil {
		return nil, err
	}
	c.WIPLimit = scanNullInt(wipLimit)

	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	if c.DeletedAt, err = scanNullTime(deleted); err != nil {
		return nil, err
	}
	return &c, nil
}

// setPositions writes a group's positions one statement at a time inside
// whatever transaction is in flight. The caller is always in one: a reorder
// half-applied is worse than a reorder refused.
func setPositions(ctx context.Context, d *DB, table string, ps []port.Position) error {
	if len(ps) == 0 {
		return nil
	}
	q := d.q(ctx)
	for _, p := range ps {
		res, err := q.ExecContext(ctx,
			`UPDATE `+table+` SET position = ? WHERE id = ?`, p.Position, p.ID)
		if err != nil {
			return fmt.Errorf("set position in %s: %w", table, err)
		}
		if err := mustAffectOne(res); err != nil {
			return err
		}
	}
	return nil
}
