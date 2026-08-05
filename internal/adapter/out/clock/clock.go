// Package clock is the real implementation of port.Clock.
package clock

import (
	"time"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

// System reads the wall clock, in UTC. Everything stored and served is UTC;
// conversion to local time happens in the browser and nowhere else
// (00-scope.md §11 question 5).
type System struct{}

var _ port.Clock = System{}

func New() System { return System{} }

func (System) Now() time.Time { return time.Now().UTC() }
