package broadcast

import (
	"context"
	"log/slog"

	adapterhttp "github.com/alejandrogmota/scrumboard-api/internal/adapter/in/http"
	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Broadcaster is the fan-out this publisher writes to. Declared here as an
// interface rather than importing the hub's concrete type, so this package
// depends on a shape it needs and not on a WebSocket library.
type Broadcaster interface {
	Broadcast(message []byte)
}

// HubPublisher renders a domain event as JSON and hands it to the hub.
//
// This is the seam the whole EventPublisher port exists for: the application
// calls Publish and never learns whether a WebSocket, a log line or nothing at
// all is on the other side (01-structure.md §1.3).
type HubPublisher struct {
	hub    Broadcaster
	logger *slog.Logger
}

var _ port.EventPublisher = (*HubPublisher)(nil)

func New(hub Broadcaster, logger *slog.Logger) *HubPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &HubPublisher{hub: hub, logger: logger}
}

// Publish never fails the operation that produced the event. A board change
// that succeeded and could not be announced is still a board change; the log
// line is what says so.
func (p *HubPublisher) Publish(ctx context.Context, e domain.Event) {
	// The wire format lives with the rest of the client contract, in the HTTP
	// adapter's dto.go. Two encoders for the same card would drift.
	message, err := adapterhttp.EncodeEvent(e)
	if err != nil {
		p.logger.ErrorContext(ctx, "could not encode an event",
			slog.String("type", string(e.Type)),
			slog.Any("error", err),
		)
		return
	}
	p.hub.Broadcast(message)
}
