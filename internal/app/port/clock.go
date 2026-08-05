package port

import "time"

// Clock is time as a dependency. Without it the last-write-wins rule
// (00-scope.md §6.5) is untestable: a test would have to sleep so two writes
// get different timestamps. With a fake, a test simply states that this write
// happened five seconds later (01-structure.md §1.6).
type Clock interface {
	Now() time.Time
}
