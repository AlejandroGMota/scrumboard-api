package fake

import (
	"context"
	"sync"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/alejandrogmota/scrumboard-api/internal/domain"
)

// Publisher records what was published. This is the one double the endpoint
// tests keep (02-testing.md §2.1): everything else in those tests is the real
// stack, but assertions on "an event was emitted, exactly once" need somewhere
// to look.
type Publisher struct {
	mu     sync.Mutex
	events []domain.Event
}

var _ port.EventPublisher = (*Publisher)(nil)

func NewPublisher() *Publisher { return &Publisher{} }

func (p *Publisher) Publish(ctx context.Context, e domain.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

// Events returns a copy of everything published so far.
func (p *Publisher) Events() []domain.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.Event(nil), p.events...)
}

// OfType returns the published events of one type.
func (p *Publisher) OfType(t domain.EventType) []domain.Event {
	var out []domain.Event
	for _, e := range p.Events() {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Last returns the most recent event, or false when nothing was published.
func (p *Publisher) Last() (domain.Event, bool) {
	events := p.Events()
	if len(events) == 0 {
		return domain.Event{}, false
	}
	return events[len(events)-1], true
}

// Reset drops the record. Handy in a test that arranges state through the API
// and only wants to assert on what the call under test emitted.
func (p *Publisher) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = nil
}
