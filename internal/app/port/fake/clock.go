package fake

import (
	"sync"
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

// Epoch is where every fake clock starts. Fixed and in UTC so golden files
// stay comparable (02-testing.md §6).
var Epoch = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// Clock is a clock advanced by hand. It is what makes the last-write-wins rule
// testable without sleeping.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

var _ port.Clock = (*Clock)(nil)

func NewClock() *Clock { return &Clock{now: Epoch} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward. "This write happened five seconds later"
// is one line, and deterministic.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set puts the clock at an exact instant.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
