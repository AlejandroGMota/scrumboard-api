package fake

import (
	"fmt"
	"sync"

	"github.com/alejandrogmota/scrumboard-api/internal/app/port"
)

// IDGen hands out id-1, id-2, id-3… Sequential and predictable, so a response
// containing a server-generated id can be compared against a golden file
// without regular expressions (02-testing.md §6).
type IDGen struct {
	mu     sync.Mutex
	n      int
	Prefix string
}

var _ port.IDGen = (*IDGen)(nil)

func NewIDGen() *IDGen { return &IDGen{Prefix: "id-"} }

func (g *IDGen) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return fmt.Sprintf("%s%d", g.Prefix, g.n)
}
