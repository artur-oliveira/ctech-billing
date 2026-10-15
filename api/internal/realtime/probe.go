package realtime

import (
	"log/slog"
	"sync"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

// The probe exists to answer one question before anything else is built on it:
// does the path from the internet to this process (Cloudflare, the load
// balancer, HAProxy, nginx) carry a WebSocket and hold it open? It is behind
// REALTIME_PROBE, off by default, and is removed once the answer is recorded in
// docs/realtime/protocol.md ("Edge").
const (
	probeAuthWait     = 5 * time.Second
	probePingInterval = 25 * time.Second
	probeLifetime     = 5 * time.Minute
	probeWriteWait    = 5 * time.Second
	probePongWait     = probePingInterval + 15*time.Second
)

// RegisterProbe mounts GET /realtime/probe.
func RegisterProbe(router fiber.Router, allowedOrigins []string) {
	upgrader := fws.FastHTTPUpgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(ctx *fasthttp.RequestCtx) bool { return originAllowed(ctx, allowedOrigins) },
	}
	router.Get("/realtime/probe", func(c fiber.Ctx) error {
		err := upgrader.Upgrade(c.RequestCtx(), func(conn *fws.Conn) {
			// Post-upgrade the handler runs outside Fiber's recover.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("realtime probe panic", "panic", r)
				}
			}()
			serveProbe(conn)
		})
		if err != nil {
			// A refused upgrade (bad origin, not a WebSocket request) has already
			// been answered by the upgrader; returning the error would make Fiber
			// replace that 403/400 with a 500.
			slog.Debug("realtime probe upgrade refused", "error", err)
		}
		return nil
	})
}

func serveProbe(conn *fws.Conn) {
	defer conn.Close()
	var mu sync.Mutex // fasthttp/websocket panics on concurrent writes
	write := func(kind int, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(probeWriteWait))
		return conn.WriteMessage(kind, data)
	}

	_ = conn.SetReadDeadline(time.Now().Add(probeAuthWait))
	_, first, err := conn.ReadMessage()
	if err != nil || len(first) == 0 {
		_ = write(fws.CloseMessage, fws.FormatCloseMessage(fws.ClosePolicyViolation, "first frame required"))
		return
	}
	if err := write(fws.BinaryMessage, first); err != nil {
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(probePongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(probePongWait))
	})

	done := make(chan struct{})
	go func() { // the read loop: it is what delivers pongs
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(probePingInterval)
	defer ping.Stop()
	life := time.NewTimer(probeLifetime)
	defer life.Stop()
	for {
		select {
		case <-ping.C:
			if err := write(fws.PingMessage, nil); err != nil {
				return
			}
		case <-life.C:
			_ = write(fws.CloseMessage, fws.FormatCloseMessage(fws.CloseNormalClosure, "probe done"))
			return
		case <-done:
			return
		}
	}
}

// originAllowed mirrors the CORS list for the upgrade: no list allows every
// origin (dev); a request with no Origin header (not a browser) is allowed.
func originAllowed(ctx *fasthttp.RequestCtx, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	origin := string(ctx.Request.Header.Peek("Origin"))
	if origin == "" {
		return true
	}
	for _, a := range allowed {
		if a == origin {
			return true
		}
	}
	return false
}
