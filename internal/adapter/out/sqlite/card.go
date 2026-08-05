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

type CardRepo struct{ d *DB }

var _ port.CardRepository = (*CardRepo)(nil)

const cardColumns = "id, column_id, title, description, label, position, created_at, updated_at, deleted_at"

func (r *CardRepo) Create(ctx context.Context, c *domain.Card) error {
	_, err := r.d.q(ctx).ExecContext(ctx,
		`INSERT INTO cards (id, column_id, title, description, label, position, created_at, updated_at, deleted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ColumnID, c.Title, c.Description, c.Label, c.Position,
		formatTime(c.CreatedAt), formatTime(c.UpdatedAt), nullTime(c.DeletedAt),
	)
	if err != nil {
		return fmt.Errorf("insert card: %w", err)
	}
	return nil
}

func (r *CardRepo) Get(ctx context.Context, id string) (*domain.Card, error) {
	return r.get(ctx, id, true)
}

func (r *CardRepo) GetAny(ctx context.Context, id string) (*domain.Card, error) {
	return r.get(ctx, id, false)
}

func (r *CardRepo) get(ctx context.Context, id string, liveOnly bool) (*domain.Card, error) {
	query := `SELECT ` + cardColumns + ` FROM cards WHERE id = ?`
	if liveOnly {
		query += ` AND deleted_at IS NULL`
	}
	c, err := scanCard(r.d.q(ctx).QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CardRepo) ListByColumn(ctx context.Context, columnID string) ([]domain.Card, error) {
	rows, err := r.d.q(ctx).QueryContext(ctx,
		`SELECT `+cardColumns+` FROM cards
		 WHERE column_id = ? AND deleted_at IS NULL
		 ORDER BY position, id`, columnID)
	if err != nil {
		return nil, fmt.Errorf("list cards: %w", err)
	}
	defer rows.Close()

	out := []domain.Card{}
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// CountByColumn is what the WIP rule reads. Soft-deleted cards do not occupy a
// slot: a column at its limit frees one the moment a card is deleted.
func (r *CardRepo) CountByColumn(ctx context.Context, columnID string) (int, error) {
	var n int
	err := r.d.q(ctx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cards WHERE column_id = ? AND deleted_at IS NULL`, columnID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count cards: %w", err)
	}
	return n, nil
}

// Update writes every mutable field including column_id, which is how a move
// between columns lands.
func (r *CardRepo) Update(ctx context.Context, c *domain.Card) error {
	res, err := r.d.q(ctx).ExecContext(ctx,
		`UPDATE cards
		 SET column_id = ?, title = ?, description = ?, label = ?, position = ?, updated_at = ?, deleted_at = ?
		 WHERE id = ?`,
		c.ColumnID, c.Title, c.Description, c.Label, c.Position,
		formatTime(c.UpdatedAt), nullTime(c.DeletedAt), c.ID,
	)
	if err != nil {
		return fmt.Errorf("update card: %w", err)
	}
	return mustAffectOne(res)
}

func (r *CardRepo) SetPositions(ctx context.Context, ps []port.Position) error {
	return setPositions(ctx, r.d, "cards", ps)
}

func (r *CardRepo) SoftDelete(ctx context.Context, id string, at time.Time) error {
	res, err := r.d.q(ctx).ExecContext(ctx,
		`UPDATE cards SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("delete card: %w", err)
	}
	return mustAffectOne(res)
}

func scanCard(s scanner) (*domain.Card, error) {
	var (
		c       domain.Card
		created string
		updated string
		deleted sql.NullString
	)
	if err := s.Scan(&c.ID, &c.ColumnID, &c.Title, &c.Description, &c.Label, &c.Position,
		&created, &updated, &deleted); err != nil {
		return nil, err
	}

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
