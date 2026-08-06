package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Sync applies a batch of operations a client queued while offline.
//
// Three rules shape it, and all three come from 02-testing.md §4.5:
//
//  1. A batch never fails as a whole because one operation did. Twenty queued
//     changes collapsing over one bad entry would leave the user stuck on
//     their phone with no way forward.
//  2. An operation already applied is skipped, not repeated. The phone that
//     lost signal mid-POST resends everything, and that has to be harmless.
//  3. The batch emits one aggregated event, not one per operation, or every
//     other device gets an event storm.
type Sync struct{ d Deps }

var _ port.SyncService = (*Sync)(nil)

func NewSync(d Deps) *Sync { return &Sync{d: d} }

// batch is the state of one call to Apply. The inner services are rebuilt per
// batch, over a publisher that swallows their events: they are cheap structs,
// and building them per call keeps two concurrent syncs from sharing a
// recorder.
type batch struct {
	d       Deps
	boards  port.BoardService
	columns port.ColumnService
	cards   port.CardService
	events  *recorder
	clock   *opClock
}

func (s *Sync) newBatch() *batch {
	rec := &recorder{boards: map[string]bool{}}
	clock := &opClock{at: s.d.Clock.Now()}

	quiet := s.d
	quiet.Events = rec
	quiet.Clock = clock

	return &batch{
		d:       s.d,
		boards:  NewBoard(quiet),
		columns: NewColumn(quiet),
		cards:   NewCard(quiet),
		events:  rec,
		clock:   clock,
	}
}

// opClock makes an applied operation carry the timestamp of when the user
// made it, not of when the queue happened to drain.
//
// This is not a detail. The conflict rule compares the stored updated_at
// against the operation's `at`, so stamping server-now would make a client
// lose against itself: a card created offline at 12:00 and edited at 12:05,
// synced at 13:00, would have updated_at = 13:00 after the create, and the
// edit that follows it in the same batch would be refused as stale. Every
// offline session of more than one operation on the same record would fail
// that way, and only on a device that had actually been offline.
//
// It also makes the comparison honest: with this, both sides of it are client
// time for changes that came through the queue.
type opClock struct{ at time.Time }

func (c *opClock) Now() time.Time { return c.at }

var _ port.Clock = (*opClock)(nil)

// Apply runs the batch in order and reports the outcome of every operation.
// The returned error is reserved for the one case that is a client programming
// error rather than a business outcome: a malformed batch, which fails whole.
func (s *Sync) Apply(ctx context.Context, ops []domain.Operation) ([]domain.OpResult, error) {
	for _, op := range ops {
		if err := validOpID(op.OpID); err != nil {
			return nil, err
		}
		if !domain.KnownOpType(op.Type) {
			return nil, domain.Invalid("type", "unknown operation type: "+string(op.Type))
		}
	}

	b := s.newBatch()
	results := make([]domain.OpResult, 0, len(ops))
	applied, failed := 0, 0

	for _, op := range ops {
		already, err := s.d.OpLog.Applied(ctx, op.OpID)
		if err != nil {
			return nil, err
		}
		if already {
			results = append(results, domain.OpResult{OpID: op.OpID, Status: domain.OpDuplicate})
			continue
		}

		res := b.applyOne(ctx, op)
		switch res.Status {
		case domain.OpApplied:
			applied++
		case domain.OpFailed, domain.OpConflict:
			failed++
		}
		results = append(results, res)
	}

	if applied > 0 {
		s.d.Events.Publish(ctx, domain.Event{
			Type:    domain.EventSyncApplied,
			BoardID: b.events.singleBoard(),
			At:      s.d.Clock.Now(),
			Payload: domain.SyncAppliedPayload{Applied: applied, Failed: failed},
		})
	}
	return results, nil
}

// applyOne runs a single operation and its op-log entry in one transaction:
// an operation that landed but was not recorded would be applied twice on the
// next resend, which is the exact failure the op log exists to prevent.
func (s *batch) applyOne(ctx context.Context, op domain.Operation) domain.OpResult {
	var state any

	// Everything this operation writes is stamped with when the user made it.
	s.clock.at = op.At

	err := s.d.Tx.WithTx(ctx, func(ctx context.Context) error {
		conflict, current, err := s.staleUpdate(ctx, op)
		if err != nil {
			return err
		}
		if conflict {
			state = current
			return errConflictResolved
		}
		st, err := s.dispatch(ctx, op)
		if err != nil {
			state = st
			return err
		}
		state = st
		return s.d.OpLog.Record(ctx, op.OpID, s.d.Clock.Now())
	})

	switch {
	case err == nil:
		return domain.OpResult{OpID: op.OpID, Status: domain.OpApplied, State: state}
	case errors.Is(err, errConflictResolved):
		return domain.OpResult{
			OpID:    op.OpID,
			Status:  domain.OpConflict,
			Code:    "stale",
			Message: "the server has a newer version of this record",
			State:   state,
		}
	default:
		return domain.OpResult{
			OpID:    op.OpID,
			Status:  domain.OpFailed,
			Code:    codeFor(err),
			Message: err.Error(),
			State:   state,
		}
	}
}

// errConflictResolved is internal: it unwinds the transaction without leaving
// the impression that something went wrong. Last-write-wins resolving in the
// server's favour is a normal outcome, not a failure.
var errConflictResolved = errors.New("server copy is newer")

// staleUpdate implements the conflict rule (00-scope.md §6.5): if the server's
// copy was modified after the client made this change, the server wins and its
// version travels back so the client can correct itself.
//
// It applies to field updates only. Positions are recalculated server-side and
// are deliberately outside last-write-wins, so moves and reorders are not
// checked here.
func (s *batch) staleUpdate(ctx context.Context, op domain.Operation) (bool, any, error) {
	switch p := op.Payload.(type) {
	case domain.BoardUpdatePayload:
		b, err := s.d.Boards.GetAny(ctx, p.ID)
		if err != nil {
			return false, nil, nil // let dispatch produce the 404
		}
		return b.UpdatedAt.After(op.At), b, nil

	case domain.ColumnUpdatePayload:
		c, err := s.d.Columns.GetAny(ctx, p.ID)
		if err != nil {
			return false, nil, nil
		}
		return c.UpdatedAt.After(op.At), c, nil

	case domain.CardUpdatePayload:
		c, err := s.d.Cards.GetAny(ctx, p.ID)
		if err != nil {
			return false, nil, nil
		}
		return c.UpdatedAt.After(op.At), c, nil
	}
	return false, nil, nil
}

// dispatch routes an operation to the service that owns it. The second return
// value is the affected entity, which travels back in the result so a client
// can reconcile without a second round trip.
func (s *batch) dispatch(ctx context.Context, op domain.Operation) (any, error) {
	switch p := op.Payload.(type) {
	case domain.BoardCreatePayload:
		b, _, err := s.boards.Create(ctx, port.CreateBoardCmd{ID: p.ID, Name: p.Name, OpID: op.OpID})
		return b, err

	case domain.BoardUpdatePayload:
		b, err := s.boards.Update(ctx, port.UpdateBoardCmd{
			ID: p.ID, Name: p.Name, LabelNames: p.LabelNames, OpID: op.OpID,
		})
		return b, err

	case domain.BoardDeletePayload:
		return nil, s.boards.Delete(ctx, port.DeleteBoardCmd{ID: p.ID, OpID: op.OpID})

	case domain.ColumnCreatePayload:
		c, _, err := s.columns.Create(ctx, port.CreateColumnCmd{
			ID: p.ID, BoardID: p.BoardID, Name: p.Name, WIPLimit: p.WIPLimit, OpID: op.OpID,
		})
		return c, err

	case domain.ColumnUpdatePayload:
		c, err := s.columns.Update(ctx, port.UpdateColumnCmd{ID: p.ID, Patch: p.Patch, OpID: op.OpID})
		return c, err

	case domain.ColumnDeletePayload:
		return nil, s.columns.Delete(ctx, port.DeleteColumnCmd{ID: p.ID, OpID: op.OpID})

	case domain.ColumnReorderPayload:
		cols, err := s.columns.Reorder(ctx, port.ReorderColumnsCmd{BoardID: p.BoardID, IDs: p.IDs, OpID: op.OpID})
		return cols, err

	case domain.CardCreatePayload:
		c, _, err := s.cards.Create(ctx, port.CreateCardCmd{
			ID: p.ID, ColumnID: p.ColumnID, Title: p.Title,
			Description: p.Description, Label: p.Label, OpID: op.OpID,
		})
		return c, err

	case domain.CardUpdatePayload:
		c, err := s.cards.Update(ctx, port.UpdateCardCmd{ID: p.ID, Patch: p.Patch, OpID: op.OpID})
		if errors.Is(err, domain.ErrNotFound) {
			// A card deleted on the server does not come back to life; the
			// response says so rather than pretending it never existed.
			if dead, getErr := s.d.Cards.GetAny(ctx, p.ID); getErr == nil && dead.Deleted() {
				return dead, errDeletedOnServer
			}
		}
		return c, err

	case domain.CardDeletePayload:
		return nil, s.cards.Delete(ctx, port.DeleteCardCmd{ID: p.ID, OpID: op.OpID})

	case domain.CardMovePayload:
		c, err := s.cards.Move(ctx, port.MoveCardCmd{
			ID: p.ID, ToColumnID: p.ToColumnID, ToIndex: p.ToIndex, OpID: op.OpID,
		})
		return c, err
	}
	return nil, domain.Invalid("payload", "does not match the operation type")
}

// errDeletedOnServer is a 404 with a reason worth telling the client apart.
var errDeletedOnServer = errors.New("the record was deleted on the server")

// codeFor gives the client something to switch on. Messages are for humans
// reading logs; codes are for code.
func codeFor(err error) string {
	switch {
	case errors.Is(err, errDeletedOnServer):
		return "deleted"
	case errors.Is(err, domain.ErrWIPExceeded):
		return "wip_exceeded"
	case errors.Is(err, domain.ErrColumnNotEmpty):
		return "column_not_empty"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	case errors.Is(err, domain.ErrInvalid):
		return "invalid"
	default:
		return "internal"
	}
}

// validOpID rejects a batch outright, unlike every other check in this file.
// An op id the client did not generate properly is a bug in the client, not a
// business case, and answering 200 to it would hide the bug.
func validOpID(id string) error {
	switch {
	case id == "":
		return domain.Invalid("op_id", "must not be empty")
	case len(id) > 64:
		return domain.Invalid("op_id", "must be at most 64 characters")
	case strings.ContainsAny(id, " \t\n\r"):
		return domain.Invalid("op_id", "must not contain whitespace")
	}
	return nil
}

// recorder is the publisher the inner services write to during a batch. It
// swallows the individual events and remembers only which boards were touched.
type recorder struct {
	mu     sync.Mutex
	boards map[string]bool
}

func (r *recorder) Publish(ctx context.Context, e domain.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.boards == nil {
		r.boards = map[string]bool{}
	}
	if e.BoardID != "" {
		r.boards[e.BoardID] = true
	}
}

// singleBoard returns the board id when the whole batch touched exactly one,
// and empty when it spanned several. A client seeing an empty board id
// refetches whatever it is showing.
func (r *recorder) singleBoard() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.boards) != 1 {
		return ""
	}
	for id := range r.boards {
		return id
	}
	return ""
}

var _ port.EventPublisher = (*recorder)(nil)
