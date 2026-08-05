// Package fake holds hand-written test doubles for the driven ports. They are
// written, not generated: the service tests are about orchestration, and a
// double whose behaviour you can read in one screen is worth more here than a
// mocking framework (02-testing.md §3.2).
package fake

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Store is an in-memory stand-in for the whole persistence layer. One Store
// backs all four repositories, because in the real adapter they share one
// database and services rely on that — a transaction has to cover writes to
// cards and columns at once.
//
// The repository ports are exposed as views over it (Boards(), Columns(),
// Cards(), OpLog()) rather than implemented on Store directly: three of them
// declare a method called Create, and a single type cannot have three.
type Store struct {
	mu      sync.Mutex
	boards  map[string]domain.Board
	columns map[string]domain.Column
	cards   map[string]domain.Card
	oplog   map[string]time.Time

	// FailNext makes the next write return this error, then clears itself.
	// Used to check that a service rolls its transaction back.
	FailNext error
}

func NewStore() *Store {
	return &Store{
		boards:  map[string]domain.Board{},
		columns: map[string]domain.Column{},
		cards:   map[string]domain.Card{},
		oplog:   map[string]time.Time{},
	}
}

func (s *Store) Boards() *BoardRepo   { return &BoardRepo{s} }
func (s *Store) Columns() *ColumnRepo { return &ColumnRepo{s} }
func (s *Store) Cards() *CardRepo     { return &CardRepo{s} }
func (s *Store) OpLog() *OpLogRepo    { return &OpLogRepo{s} }

var (
	_ port.TxManager        = (*Store)(nil)
	_ port.BoardRepository  = (*BoardRepo)(nil)
	_ port.ColumnRepository = (*ColumnRepo)(nil)
	_ port.CardRepository   = (*CardRepo)(nil)
	_ port.OpLogRepository  = (*OpLogRepo)(nil)
)

// --- TxManager ------------------------------------------------------------

// WithTx snapshots the store, runs fn, and restores the snapshot if fn fails.
// A fake that always commits would let a service pass a test the real SQLite
// adapter would fail.
func (s *Store) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	s.mu.Lock()
	snap := s.snapshot()
	s.mu.Unlock()

	if err := fn(ctx); err != nil {
		s.mu.Lock()
		s.restore(snap)
		s.mu.Unlock()
		return err
	}
	return nil
}

type snapshot struct {
	boards  map[string]domain.Board
	columns map[string]domain.Column
	cards   map[string]domain.Card
	oplog   map[string]time.Time
}

func (s *Store) snapshot() snapshot {
	return snapshot{
		boards:  copyMap(s.boards),
		columns: copyMap(s.columns),
		cards:   copyMap(s.cards),
		oplog:   copyMap(s.oplog),
	}
}

func (s *Store) restore(snap snapshot) {
	s.boards, s.columns, s.cards, s.oplog = snap.boards, snap.columns, snap.cards, snap.oplog
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// fail must be called with the mutex held.
func (s *Store) fail() error {
	if s.FailNext != nil {
		err := s.FailNext
		s.FailNext = nil
		return err
	}
	return nil
}

// --- boards ---------------------------------------------------------------

type BoardRepo struct{ s *Store }

func (r *BoardRepo) Create(ctx context.Context, b *domain.Board) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	r.s.boards[b.ID] = *b
	return nil
}

func (r *BoardRepo) Get(ctx context.Context, id string) (*domain.Board, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	b, ok := r.s.boards[id]
	if !ok || b.DeletedAt != nil {
		return nil, domain.ErrNotFound
	}
	return &b, nil
}

func (r *BoardRepo) GetAny(ctx context.Context, id string) (*domain.Board, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	b, ok := r.s.boards[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &b, nil
}

func (r *BoardRepo) GetWithChildren(ctx context.Context, id string) (*domain.Board, error) {
	b, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cols, err := r.s.Columns().ListByBoard(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range cols {
		cards, err := r.s.Cards().ListByColumn(ctx, cols[i].ID)
		if err != nil {
			return nil, err
		}
		cols[i].Cards = cards
	}
	b.Columns = cols
	return b, nil
}

func (r *BoardRepo) List(ctx context.Context) ([]domain.Board, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := []domain.Board{}
	for _, b := range r.s.boards {
		if b.DeletedAt == nil {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (r *BoardRepo) Update(ctx context.Context, b *domain.Board) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	if _, ok := r.s.boards[b.ID]; !ok {
		return domain.ErrNotFound
	}
	r.s.boards[b.ID] = *b
	return nil
}

// SoftDelete cascades to the board's columns and their cards.
func (r *BoardRepo) SoftDelete(ctx context.Context, id string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	b, ok := r.s.boards[id]
	if !ok {
		return domain.ErrNotFound
	}
	if b.DeletedAt == nil {
		b.DeletedAt = &at
		r.s.boards[id] = b
	}
	for cid, c := range r.s.columns {
		if c.BoardID != id || c.DeletedAt != nil {
			continue
		}
		c.DeletedAt = &at
		r.s.columns[cid] = c
		for kid, k := range r.s.cards {
			if k.ColumnID == cid && k.DeletedAt == nil {
				k.DeletedAt = &at
				r.s.cards[kid] = k
			}
		}
	}
	return nil
}

// --- columns --------------------------------------------------------------

type ColumnRepo struct{ s *Store }

func (r *ColumnRepo) Create(ctx context.Context, c *domain.Column) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	r.s.columns[c.ID] = *c
	return nil
}

func (r *ColumnRepo) Get(ctx context.Context, id string) (*domain.Column, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	c, ok := r.s.columns[id]
	if !ok || c.DeletedAt != nil {
		return nil, domain.ErrNotFound
	}
	return &c, nil
}

func (r *ColumnRepo) GetAny(ctx context.Context, id string) (*domain.Column, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	c, ok := r.s.columns[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &c, nil
}

func (r *ColumnRepo) ListByBoard(ctx context.Context, boardID string) ([]domain.Column, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := []domain.Column{}
	for _, c := range r.s.columns {
		if c.BoardID == boardID && c.DeletedAt == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

func (r *ColumnRepo) Update(ctx context.Context, c *domain.Column) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	if _, ok := r.s.columns[c.ID]; !ok {
		return domain.ErrNotFound
	}
	r.s.columns[c.ID] = *c
	return nil
}

func (r *ColumnRepo) SetPositions(ctx context.Context, ps []port.Position) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	for _, p := range ps {
		c, ok := r.s.columns[p.ID]
		if !ok {
			return domain.ErrNotFound
		}
		c.Position = p.Position
		r.s.columns[p.ID] = c
	}
	return nil
}

func (r *ColumnRepo) SoftDelete(ctx context.Context, id string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	c, ok := r.s.columns[id]
	if !ok {
		return domain.ErrNotFound
	}
	if c.DeletedAt == nil {
		c.DeletedAt = &at
		r.s.columns[id] = c
	}
	return nil
}

// --- cards ----------------------------------------------------------------

type CardRepo struct{ s *Store }

func (r *CardRepo) Create(ctx context.Context, c *domain.Card) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	r.s.cards[c.ID] = *c
	return nil
}

func (r *CardRepo) Get(ctx context.Context, id string) (*domain.Card, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	c, ok := r.s.cards[id]
	if !ok || c.DeletedAt != nil {
		return nil, domain.ErrNotFound
	}
	return &c, nil
}

func (r *CardRepo) GetAny(ctx context.Context, id string) (*domain.Card, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	c, ok := r.s.cards[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &c, nil
}

func (r *CardRepo) ListByColumn(ctx context.Context, columnID string) ([]domain.Card, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := []domain.Card{}
	for _, c := range r.s.cards {
		if c.ColumnID == columnID && c.DeletedAt == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

func (r *CardRepo) CountByColumn(ctx context.Context, columnID string) (int, error) {
	cards, err := r.ListByColumn(ctx, columnID)
	return len(cards), err
}

func (r *CardRepo) Update(ctx context.Context, c *domain.Card) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	if _, ok := r.s.cards[c.ID]; !ok {
		return domain.ErrNotFound
	}
	r.s.cards[c.ID] = *c
	return nil
}

func (r *CardRepo) SetPositions(ctx context.Context, ps []port.Position) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	for _, p := range ps {
		c, ok := r.s.cards[p.ID]
		if !ok {
			return domain.ErrNotFound
		}
		c.Position = p.Position
		r.s.cards[p.ID] = c
	}
	return nil
}

func (r *CardRepo) SoftDelete(ctx context.Context, id string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if err := r.s.fail(); err != nil {
		return err
	}
	c, ok := r.s.cards[id]
	if !ok {
		return domain.ErrNotFound
	}
	if c.DeletedAt == nil {
		c.DeletedAt = &at
		r.s.cards[id] = c
	}
	return nil
}

// --- op log ---------------------------------------------------------------

type OpLogRepo struct{ s *Store }

func (r *OpLogRepo) Applied(ctx context.Context, opID string) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	_, ok := r.s.oplog[opID]
	return ok, nil
}

func (r *OpLogRepo) Record(ctx context.Context, opID string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.oplog[opID] = at
	return nil
}
