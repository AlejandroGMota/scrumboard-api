package domain

import "time"

// EventType names a change that connected clients care about.
type EventType string

const (
	EventBoardCreated   EventType = "board.created"
	EventBoardUpdated   EventType = "board.updated"
	EventBoardDeleted   EventType = "board.deleted"
	EventColumnCreated  EventType = "column.created"
	EventColumnUpdated  EventType = "column.updated"
	EventColumnDeleted  EventType = "column.deleted"
	EventColumnsOrdered EventType = "columns.ordered"
	EventCardCreated    EventType = "card.created"
	EventCardUpdated    EventType = "card.updated"
	EventCardDeleted    EventType = "card.deleted"
	EventCardMoved      EventType = "card.moved"
	// EventSyncApplied is the single aggregated event a sync batch emits.
	// One event per operation would give the other devices an event storm
	// (02-testing.md §4.5).
	EventSyncApplied EventType = "sync.applied"
)

// Event is what gets broadcast when something changes. The app builds these
// and hands them to an EventPublisher; whether a WebSocket hub, a log or
// nothing sits behind that port is not the app's concern.
type Event struct {
	Type    EventType
	BoardID string
	At      time.Time

	// OpID is the client-supplied id of the request that caused the change,
	// echoed back so the originating client can ignore its own event
	// (00-scope.md §8). Empty when the change came from something that sent
	// no op id.
	OpID string

	// Payload is the affected entity (*Board, *Column, *Card) or, for
	// columns.ordered and sync.applied, the shapes below.
	Payload any
}

// CardMovedPayload carries the card in its new home plus where it came from.
// A client that only received the card would have to guess which column to
// remove it from, and guessing is how a card ends up drawn twice.
type CardMovedPayload struct {
	Card         *Card
	FromColumnID string
	ToColumnID   string
}

// ColumnsOrderedPayload carries the board's column ids in their new order.
type ColumnsOrderedPayload struct {
	BoardID string
	IDs     []string
}

// SyncAppliedPayload tells other devices that a batch landed, and how much of
// it did. They refetch the board rather than replaying operations they never
// saw queued.
type SyncAppliedPayload struct {
	Applied int
	Failed  int
}
