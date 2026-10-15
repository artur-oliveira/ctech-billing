package realtime

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
)

// WebSocket message types and close codes (RFC 6455). They are numbers here so
// the hub does not depend on a WebSocket library: the gateway adapts one.
const (
	msgBinary = 2
	msgClose  = 8

	CloseNormal    = 1000
	CloseGoingAway = 1001 // draining
	ClosePolicy    = 1008 // unauthorized
	CloseInternal  = 1011
	CloseTryLater  = 1013 // a slow consumer
)

// Gap and Bye reasons carried on the wire.
const (
	GapCursorTooOld = "cursor_too_old"
	GapSlowConsumer = "slow_consumer"
	GapReplayLimit  = "replay_limit"

	ByeTokenExpired = "token_expired"
	ByeRevoked      = "revoked"
	ByeDraining     = "draining"
	ByeLimit        = "limit"
	ByeUnauthorized = "unauthorized"
)

// Socket is the part of a WebSocket the hub writes through. The gateway adapts
// a fasthttp/websocket connection to it; tests use a fake.
type Socket interface {
	WriteMessage(messageType int, data []byte) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

// Conn is one live connection: a principal, a bounded send queue and the single
// goroutine that writes to its socket. Nothing else ever writes to the socket,
// which is what keeps fasthttp/websocket from panicking on concurrent writes.
type Conn struct {
	ID        string
	Principal *Principal
	IP        string

	sock      Socket
	queue     chan []byte
	writeWait time.Duration
	metrics   *Metrics

	mu        sync.Mutex
	finishing bool
	final     []byte
	code      int
	reason    string
	slow      atomic.Bool
	finish    chan struct{}
	done      chan struct{}
}

// NewConn starts the connection's writer. queueSize bounds what a slow reader can
// make the server hold: beyond it the connection is closed, never waited for.
func NewConn(id string, p *Principal, ip string, sock Socket, queueSize int, writeWait time.Duration, m *Metrics) *Conn {
	c := &Conn{
		ID: id, Principal: p, IP: ip, sock: sock, queue: make(chan []byte, queueSize),
		writeWait: writeWait, metrics: m, finish: make(chan struct{}), done: make(chan struct{}),
	}
	go c.run()
	return c
}

// ErrConnClosed is a send to a connection that is finishing.
var ErrConnClosed = errors.New("realtime: connection closed")

// WriteMessage lets the registry (ws.Conn) deliver a broadcast frame. The message
// type is ignored: every frame on this socket is binary protobuf.
func (c *Conn) WriteMessage(_ int, data []byte) error {
	if !c.Send(data) {
		return ErrConnClosed
	}
	return nil
}

// Send queues a frame without ever blocking. False means it was not queued: the
// connection is finishing, or its queue was full, in which case it is now being
// closed with a Gap (a reader that cannot keep up must reload, not be waited for).
func (c *Conn) Send(frame []byte) bool {
	c.mu.Lock()
	if c.finishing {
		c.mu.Unlock()
		return false
	}
	select {
	case c.queue <- frame:
		c.mu.Unlock()
		return true
	default:
	}
	c.mu.Unlock()
	c.metrics.slowClose()
	c.metrics.gap(GapSlowConsumer)
	c.slow.Store(true)
	c.Finish(gapFrame(GapSlowConsumer), CloseTryLater, GapSlowConsumer)
	return false
}

// Finish ends the connection: what is queued is written (unless the reader was
// too slow, then it is dropped), then final if any, then a close frame. It does
// not wait, and calling it again does nothing.
func (c *Conn) Finish(final []byte, code int, reason string) {
	c.mu.Lock()
	if c.finishing {
		c.mu.Unlock()
		return
	}
	c.finishing, c.final, c.code, c.reason = true, final, code, reason
	c.mu.Unlock()
	if reason != "" {
		c.metrics.bye(reason)
	}
	close(c.finish)
}

// Done is closed when the writer has stopped and the socket is closed.
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) write(kind int, data []byte) error {
	_ = c.sock.SetWriteDeadline(time.Now().Add(c.writeWait))
	return c.sock.WriteMessage(kind, data)
}

func (c *Conn) run() {
	defer close(c.done)
	defer c.sock.Close()
	for {
		select {
		case frame := <-c.queue:
			if c.slow.Load() {
				continue // the reader fell behind: what it missed is replayed from the log
			}
			if err := c.write(msgBinary, frame); err != nil {
				return
			}
			c.metrics.frameSent()
			c.metrics.observeWrite(frame, time.Now())
		case <-c.finish:
			c.mu.Lock()
			final, code, reason := c.final, c.code, c.reason
			c.mu.Unlock()
			if !c.slow.Load() {
				for drained := false; !drained; {
					select {
					case frame := <-c.queue:
						if err := c.write(msgBinary, frame); err != nil {
							return
						}
					default:
						drained = true
					}
				}
			}
			if final != nil {
				if err := c.write(msgBinary, final); err != nil {
					return
				}
			}
			_ = c.write(msgClose, closePayload(code, reason))
			return
		}
	}
}

// closePayload is a WebSocket close frame body: a 2-byte code and a reason.
func closePayload(code int, reason string) []byte {
	b := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(b, uint16(code))
	return append(b, reason...)
}

func gapFrame(reason string) []byte {
	b, _ := proto.Marshal(&wire.ServerFrame{Body: &wire.ServerFrame_Gap{Gap: &wire.Gap{Reason: reason}}})
	return b
}

// ByeFrame is the frame that tells a client why it is being disconnected.
func ByeFrame(reason string, retryAfter time.Duration) []byte {
	b, _ := proto.Marshal(&wire.ServerFrame{Body: &wire.ServerFrame_Bye{Bye: &wire.Bye{
		Reason: reason, RetryAfterMs: uint32(retryAfter.Milliseconds()),
	}}})
	return b
}
