package port

import (
	"context"

	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// EventPublisher receives every change the application makes. The app never
// learns whether a WebSocket hub, a log line or nothing at all is behind it —
// which is the point: if the WebSocket turns out to be work brought forward
// (00-scope.md §6.2), it is removed by swapping one adapter.
//
// Publish must not fail the operation that produced the event. Implementations
// that can block — a hub with a slow client — drop rather than wait.
type EventPublisher interface {
	Publish(ctx context.Context, e domain.Event)
}
