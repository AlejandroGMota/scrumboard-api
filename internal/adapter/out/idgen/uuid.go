// Package idgen is the real implementation of port.IDGen.
package idgen

import (
	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
	"github.com/google/uuid"
)

// UUIDv7 generates time-ordered UUIDs, the same kind the PWA generates for
// itself while offline (01-structure.md §1.5).
//
// v7 rather than v4 because it sorts by creation time: rows land in the index
// in order instead of scattering, and a list of ids is readable as a timeline.
type UUIDv7 struct{}

var _ port.IDGen = UUIDv7{}

func New() UUIDv7 { return UUIDv7{} }

// New returns a v7 UUID, falling back to v4 if the system clock or entropy
// source misbehaves. An id is required to continue; its sort order is not.
func (UUIDv7) New() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
