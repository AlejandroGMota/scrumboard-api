package ws

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// sendBuffer is how far behind a client may fall before it is dropped.
	// Small on purpose: a client this far behind has stopped reading, and
	// buffering more only delays noticing.
	sendBuffer = 32

	// pingInterval keeps intermediaries from closing an idle connection. The
	// host's nginx is configured with proxy_read_timeout 3600s, but a phone's
	// mobile network has opinions of its own.
	pingInterval = 30 * time.Second

	// writeTimeout bounds a single write. Without it a half-open connection —
	// the phone that walked out of range without closing anything — holds a
	// goroutine until the OS gives up, which can be minutes.
	writeTimeout = 10 * time.Second
)

// Client is one connected browser: a connection, a buffered outbox, and the
// two pumps that serve them.
type Client struct {
	conn   *websocket.Conn
	send   chan []byte
	logger *slog.Logger

	closeOnce sync.Once
	done      chan struct{}
}

func newClient(conn *websocket.Conn, logger *slog.Logger) *Client {
	return &Client{
		conn:   conn,
		send:   make(chan []byte, sendBuffer),
		logger: logger,
		done:   make(chan struct{}),
	}
}

// trySend queues a message, reporting false when the buffer is full.
//
// The non-blocking send is the whole slow-client policy: the hub drops a
// client that cannot keep up rather than waiting for it.
func (c *Client) trySend(message []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case c.send <- message:
		return true
	default:
		return false
	}
}

// close tears the connection down. It never blocks, which is not a detail:
//
// the graceful Close writes a close frame and then waits for the peer to send
// one back. A client that has stopped reading — the exact client the hub wants
// to drop — never will, so a graceful close there blocks the fan-out to every
// other device for the length of the close timeout. CloseNow drops the TCP
// connection instead. The browser sees an abnormal close and reconnects with
// backoff, which is what it would do anyway.
func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		//nolint:errcheck // the connection is going away regardless
		c.conn.CloseNow()
	})
}

// writePump drains the outbox and pings on a timer. One goroutine owns every
// write to the connection: the library allows exactly one writer at a time,
// and funnelling them through here is what guarantees it.
func (c *Client) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	defer c.close()

	for {
		select {
		case <-ctx.Done():
			return

		case <-c.done:
			return

		case message := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Write(writeCtx, websocket.MessageText, message)
			cancel()
			if err != nil {
				c.logger.Debug("websocket write failed", slog.Any("error", err))
				return
			}

		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				c.logger.Debug("websocket ping failed", slog.Any("error", err))
				return
			}
		}
	}
}

// readPump exists to notice that the client went away.
//
// The board is broadcast-only: the browser sends changes over HTTP, not over
// this socket. But a connection nobody reads from never surfaces a close
// frame, and the client would linger in the hub until its next failed write.
// Reading and discarding is what makes a disconnect immediate.
func (c *Client) readPump(ctx context.Context) {
	defer c.close()

	// A client that tries to send anything is either confused or hostile;
	// neither deserves a large buffer.
	c.conn.SetReadLimit(4096)

	for {
		if _, _, err := c.conn.Read(ctx); err != nil {
			return
		}
	}
}
