// Package broadcast implements port.EventPublisher.
package broadcast

import (
	"context"
	"log/slog"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// LoggerPublisher writes each event to the log and nothing more.
//
// It is what the API ships with before the WebSocket exists (step 4 of the
// build order), and it is the proof that the port is worth having: the
// application publishes events either way and never learns the difference.
// It also stays useful afterwards, as the thing to swap in when the hub is
// suspected of something.
type LoggerPublisher struct{ logger *slog.Logger }

var _ port.EventPublisher = (*LoggerPublisher)(nil)

func NewLogger(logger *slog.Logger) *LoggerPublisher {
	return &LoggerPublisher{logger: logger}
}

func (p *LoggerPublisher) Publish(ctx context.Context, e domain.Event) {
	p.logger.LogAttrs(ctx, slog.LevelDebug, "event",
		slog.String("type", string(e.Type)),
		slog.String("board_id", e.BoardID),
		slog.String("op_id", e.OpID),
	)
}
