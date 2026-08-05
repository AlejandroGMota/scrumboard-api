// Package ws is the WebSocket adapter: a hub, its clients, and the handshake.
package ws

import (
	"context"
	"log/slog"
	"sync"
)

// Hub keeps the set of connected clients and fans events out to them.
//
// It is a plain mutex-guarded map rather than the channel-based registry the
// Gorilla example made famous. With one user and a handful of devices, a
// select loop is machinery without a job; a mutex held for the length of a
// map iteration is easier to read and easier to prove right under -race.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	logger  *slog.Logger

	closed bool
}

func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{clients: map[*Client]struct{}{}, logger: logger}
}

// Run closes every client when ctx is cancelled, so a shutdown does not leave
// browsers holding a connection to a process that has stopped answering.
func (h *Hub) Run(ctx context.Context) {
	<-ctx.Done()

	h.mu.Lock()
	h.closed = true
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = map[*Client]struct{}{}
	h.mu.Unlock()

	for _, c := range clients {
		c.close()
	}
	h.logger.Info("websocket hub stopped", slog.Int("clients", len(clients)))
}

func (h *Hub) add(c *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.clients[c] = struct{}{}
	return true
}

func (h *Hub) remove(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Count reports how many clients are connected. Used by the tests, and by
// nothing else.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast sends a message to every connected client, including the one whose
// request produced it.
//
// Echoing to the originator is deliberate (00-scope.md §8): the hub stays
// simpler, the client already filters its own events by op_id, and a client
// whose HTTP response was lost still converges instead of sitting on a stale
// board for ever.
//
// It never blocks. A client that has stopped reading gets dropped rather than
// stalling the fan-out — one phone that went to sleep mid-connection must not
// freeze the laptop.
func (h *Hub) Broadcast(message []byte) {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	var stalled []*Client
	for _, c := range clients {
		if !c.trySend(message) {
			stalled = append(stalled, c)
		}
	}

	for _, c := range stalled {
		h.logger.Warn("dropping a client whose send buffer filled up")
		h.remove(c)
		c.close()
	}
}
