package realtime

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/api-commons/drain"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// ByeUnavailable is an upstream (ctech-account, a table) being down while a
// connection was being authorized: the client retries.
const ByeUnavailable = "unavailable"

// ByeBadFrame is a frame after authentication that is not a ClientFrame.
const ByeBadFrame = "bad_frame"

// ProtocolVersion is what Ready announces (docs/realtime/protocol.md).
const ProtocolVersion = 1

// Config holds the gateway's timings and limits. The zero value is not usable;
// start from DefaultConfig. Tests shorten the durations.
type Config struct {
	// AuthWait is how long a socket may stay silent before its first frame.
	AuthWait time.Duration
	// FirstFrameMax and ReadMax are the biggest frame accepted before and after
	// authentication.
	FirstFrameMax int64
	ReadMax       int64
	// PingInterval is the native ping. Cloudflare drops a WebSocket idle for about
	// 100 s, so it must stay well under that. PongWait is how long a silent client
	// is tolerated.
	PingInterval time.Duration
	PongWait     time.Duration
	// TickInterval sends the head of the change log for the connection's scopes.
	TickInterval time.Duration
	// RecheckInterval re-asks whether a console member is still a member.
	RecheckInterval time.Duration
	// ReplayMax is the most changes a resume replays; beyond it the client reloads.
	// ReplaysPerMinute caps how often one principal may ask.
	ReplayMax        int
	ReplaysPerMinute int
	// HeadTTL is how long the head of a scope set is reused across connections.
	HeadTTL time.Duration
	// DrainRetry is the retry-after sent with Bye draining.
	DrainRetry time.Duration
	// AuthorizeTimeout bounds the upstream calls of one authorization.
	AuthorizeTimeout time.Duration
	// DegradedPoll is the interval the client is asked to poll at while the
	// fan-out is down (Hint).
	DegradedPoll time.Duration
	// FramesPerSecond caps what one connection may send; beyond it (a burst of twice
	// as many is tolerated) the connection is closed with Bye limit. A client sends a
	// ping every half minute: the cap is for the ones that do not behave. Zero is off.
	FramesPerSecond int
}

// DefaultConfig is the production configuration.
func DefaultConfig() Config {
	return Config{
		AuthWait: 5 * time.Second, FirstFrameMax: 32 << 10, ReadMax: 32 << 10,
		PingInterval: 25 * time.Second, PongWait: 40 * time.Second,
		TickInterval: 30 * time.Second, RecheckInterval: 5 * time.Minute,
		ReplayMax: 500, ReplaysPerMinute: 3, HeadTTL: 10 * time.Second,
		DrainRetry: 3 * time.Second, AuthorizeTimeout: 5 * time.Second, DegradedPoll: 10 * time.Second,
		FramesPerSecond: 20,
	}
}

// ChangeLog is the read side of the change log (repositories.RealtimeRepository).
type ChangeLog interface {
	Since(ctx context.Context, scopes []string, cursor string, limit int) ([]repositories.Change, bool, error)
	Head(ctx context.Context, scopes []string) (string, error)
}

// Gateway serves GET /v1.0/ws: it authenticates a connection by its first frame,
// registers it under the scopes its principal owns, replays what it missed from
// its cursor, and keeps it honest (heartbeat, expiry, membership, backpressure).
type Gateway struct {
	// Authorizers are keyed by Select's answer: console, portal, checkout.
	Authorizers    map[string]Authorizer
	Hub            *Hub
	Log            ChangeLog
	AllowedOrigins []string
	Drain          *drain.Tracker
	Config         Config
	Now            func() time.Time
	// Health, when set, reports a fan-out that is not working; the gateway then
	// asks its clients to poll too (Hint) until it recovers.
	Health interface{ Degraded() bool }

	replays *replayLimiter
	heads   *headCache
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// RegisterGateway mounts GET /ws on the router (the same path as the wallet and
// Poker).
func RegisterGateway(router fiber.Router, g *Gateway) {
	g.replays = newReplayLimiter(g.Config.ReplaysPerMinute, time.Minute)
	g.heads = newHeadCache(g.Config.HeadTTL)
	upgrader := fws.FastHTTPUpgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
			return OriginAllowed(string(ctx.Request.Header.Peek("Origin")), g.AllowedOrigins)
		},
	}
	router.Get("/ws", func(c fiber.Ctx) error {
		ip := c.IP()
		err := upgrader.Upgrade(c.RequestCtx(), func(conn *fws.Conn) {
			// Post-upgrade the handler runs outside Fiber's recover: an unrecovered
			// panic here would take the whole process down, not one connection.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("realtime gateway panic", "panic", r)
					_ = conn.Close()
				}
			}()
			g.serve(conn, ip)
		})
		if err != nil {
			// A refused upgrade (a bad origin, not a WebSocket request) was already
			// answered by the upgrader; returning the error would turn its 403 or
			// 400 into a 500.
			slog.Debug("realtime upgrade refused", "error", err)
		}
		return nil
	})
}

// OriginAllowed mirrors the CORS list for the upgrade. With a list configured, a
// missing Origin is refused too: a browser always sends one, and a request
// without it is not the console. With no list (development) everything passes.
func OriginAllowed(origin string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	return origin != "" && slices.Contains(allowed, origin)
}

// fwsSocket adapts a fasthttp/websocket connection to Socket. Ping is a control
// frame, which the library allows concurrently with the hub's writer.
type fwsSocket struct{ conn *fws.Conn }

func (s fwsSocket) WriteMessage(kind int, data []byte) error { return s.conn.WriteMessage(kind, data) }
func (s fwsSocket) SetWriteDeadline(t time.Time) error       { return s.conn.SetWriteDeadline(t) }
func (s fwsSocket) Close() error                             { return s.conn.Close() }
func (s fwsSocket) Ping(wait time.Duration) error {
	return s.conn.WriteControl(fws.PingMessage, nil, time.Now().Add(wait))
}

// refuse answers a connection that never got a hub entry: a Bye and a close,
// written directly because nothing else is writing yet.
func refuse(conn *fws.Conn, reason string, code int, retry time.Duration) {
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = conn.WriteMessage(fws.BinaryMessage, ByeFrame(reason, retry))
	_ = conn.WriteMessage(fws.CloseMessage, closePayload(code, reason))
	_ = conn.Close()
}

// readFirst reads the first frame under AuthWait and the first-frame size cap.
func (g *Gateway) readFirst(conn *fws.Conn) (*wire.Auth, bool) {
	conn.SetReadLimit(g.Config.FirstFrameMax)
	_ = conn.SetReadDeadline(time.Now().Add(g.Config.AuthWait))
	kind, raw, err := conn.ReadMessage()
	if err != nil || kind != fws.BinaryMessage {
		return nil, false
	}
	var frame wire.ClientFrame
	if proto.Unmarshal(raw, &frame) != nil || frame.GetAuth() == nil {
		return nil, false
	}
	return frame.GetAuth(), true
}

func (g *Gateway) serve(conn *fws.Conn, ip string) {
	auth, ok := g.readFirst(conn)
	if !ok {
		refuse(conn, ByeUnauthorized, ClosePolicy, 0)
		return
	}
	authz := g.Authorizers[Select(auth)]
	if authz == nil {
		refuse(conn, ByeUnauthorized, ClosePolicy, 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), g.Config.AuthorizeTimeout)
	p, err := authz.Authorize(ctx, auth)
	cancel()
	switch {
	case err == nil:
	case errors.Is(err, ErrUnauthorized):
		g.Hub.metrics.authFailure("unauthorized")
		refuse(conn, ByeUnauthorized, ClosePolicy, 0)
		return
	default:
		g.Hub.metrics.authFailure("unavailable")
		refuse(conn, ByeUnavailable, CloseInternal, 5*time.Second)
		return
	}

	connID := id.New()
	c := g.Hub.NewConn(connID, p, ip, fwsSocket{conn})
	// Registered with the drain tracker before the hub: a process that is already
	// draining turns the connection away instead of adopting it.
	if g.Drain != nil {
		if !g.Drain.Register(connID, func(context.Context) error {
			c.Finish(ByeFrame(ByeDraining, g.Config.DrainRetry), CloseGoingAway, ByeDraining)
			return nil
		}) {
			c.Finish(ByeFrame(ByeDraining, g.Config.DrainRetry), CloseGoingAway, ByeDraining)
			<-c.Done()
			return
		}
		defer g.Drain.Unregister(connID)
	}

	// Ready is queued before the connection is registered, so it is the first
	// frame any client reads; changes published from here on follow it.
	c.Send(readyFrame(g.now(), kindsOf(p.Scopes)))
	g.hint(c)
	if err := g.Hub.Add(c, p.Scopes); err != nil {
		c.Finish(ByeFrame(ByeLimit, 0), ClosePolicy, ByeLimit)
		<-c.Done()
		return
	}
	defer func() {
		g.Hub.Remove(c)
		c.Finish(nil, CloseNormal, "")
		<-c.Done()
	}()

	g.replay(c, p, auth.GetResumeCursor())

	reauth := make(chan time.Time, 1)
	go g.watch(c, p, authz, fwsSocket{conn}, reauth)
	g.readLoop(conn, c, p, authz, reauth)
}

// hint asks the client to poll as well while the fan-out is down. It says it
// again on every Tick and stops saying it the moment the fan-out recovers, which
// is how a client learns it can stop polling.
func (g *Gateway) hint(c *Conn) {
	if g.Health == nil || !g.Health.Degraded() {
		return
	}
	b, _ := proto.Marshal(&wire.ServerFrame{Body: &wire.ServerFrame_Hint{Hint: &wire.Hint{
		PollIntervalMs: uint32(g.Config.DegradedPoll.Milliseconds()),
	}}})
	c.Send(b)
}

func readyFrame(now time.Time, kinds []string) []byte {
	b, _ := proto.Marshal(&wire.ServerFrame{Body: &wire.ServerFrame_Ready{Ready: &wire.Ready{
		ProtocolVersion: ProtocolVersion, ServerTimeMs: uint64(now.UnixMilli()), ScopeKinds: kinds,
	}}})
	return b
}

func kindsOf(scopes []string) []string {
	var out []string
	for _, s := range scopes {
		if k := Kind(s); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// replay sends what the client missed since its cursor, from the log: a Gap when
// it asked too often, too long ago, or for too much, and the changes otherwise.
func (g *Gateway) replay(c *Conn, p *Principal, cursor string) {
	if cursor == "" {
		return
	}
	if !g.replays.allow(principalKey(p), g.now()) {
		g.Hub.metrics.gap(GapReplayLimit)
		c.Send(gapFrame(GapReplayLimit))
		return
	}
	if tooOld, err := repositories.CursorTooOld(cursor, g.now()); err != nil || tooOld {
		g.Hub.metrics.gap(GapCursorTooOld)
		c.Send(gapFrame(GapCursorTooOld))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.Config.AuthorizeTimeout)
	defer cancel()
	changes, more, err := g.Log.Since(ctx, p.Scopes, cursor, g.Config.ReplayMax)
	switch {
	case err != nil:
		// A log that cannot be read is a client that cannot be caught up: make it
		// reload rather than let it believe it is current.
		slog.Warn("realtime: replay failed", "error", err)
		g.Hub.metrics.gap(GapCursorTooOld)
		c.Send(gapFrame(GapCursorTooOld))
		return
	case more:
		g.Hub.metrics.gap(GapReplayLimit)
		c.Send(gapFrame(GapReplayLimit))
		return
	}
	g.Hub.metrics.replay(len(changes))
	for i := range changes {
		frame, err := FrameOf(&changes[i])
		if err != nil {
			continue
		}
		c.Send(frame)
	}
}

// readLoop reads the client's frames until the socket ends. A pong or any frame
// extends the deadline; a client that goes quiet past PongWait is dropped.
func (g *Gateway) readLoop(conn *fws.Conn, c *Conn, p *Principal, authz Authorizer, reauth chan time.Time) {
	conn.SetReadLimit(g.Config.ReadMax)
	extend := func() { _ = conn.SetReadDeadline(time.Now().Add(g.Config.PongWait)) }
	extend()
	conn.SetPongHandler(func(string) error { extend(); return nil })
	budget := newFrameBudget(g.Config.FramesPerSecond, g.now)
	for {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		extend()
		if !budget.take() {
			c.Finish(ByeFrame(ByeLimit, 0), ClosePolicy, ByeLimit)
			return
		}
		if kind != fws.BinaryMessage {
			c.Finish(ByeFrame(ByeBadFrame, 0), fws.CloseUnsupportedData, ByeBadFrame)
			return
		}
		var frame wire.ClientFrame
		if proto.Unmarshal(raw, &frame) != nil {
			c.Finish(ByeFrame(ByeBadFrame, 0), fws.CloseUnsupportedData, ByeBadFrame)
			return
		}
		switch {
		case frame.GetPing() != nil:
			pong, _ := proto.Marshal(&wire.ServerFrame{Body: &wire.ServerFrame_Pong{Pong: &wire.Pong{
				ClientTimeMs: frame.GetPing().GetClientTimeMs(), ServerTimeMs: uint64(g.now().UnixMilli()),
			}}})
			c.Send(pong)
		case frame.GetReauth() != nil:
			if !g.reauthorize(c, p, authz, frame.GetReauth().GetToken(), reauth) {
				return
			}
		}
		// Anything else (a second Auth, a field from a newer client) is ignored: an
		// old server keeps working with a newer client.
	}
}

// reauthorize swaps the token a connection runs on. The new token must be the
// same person for the same scopes: a Reauth can extend a connection, never change
// who it belongs to.
func (g *Gateway) reauthorize(c *Conn, p *Principal, authz Authorizer, token string, reauth chan time.Time) bool {
	if p.Kind == KindCheckout || token == "" {
		return true // a pay link is not renewed; the page reconnects
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.Config.AuthorizeTimeout)
	defer cancel()
	next, err := authz.Authorize(ctx, &wire.Auth{Token: token, Space: selectorString(p), Mode: modeName(p.live)})
	switch {
	case errors.Is(err, ErrUnavailable):
		return true // keep the connection and let the old expiry stand; it will ask again
	case err != nil || next.Sub != p.Sub || !slices.Equal(next.Scopes, p.Scopes):
		c.Finish(ByeFrame(ByeUnauthorized, 0), ClosePolicy, ByeUnauthorized)
		return false
	}
	select {
	case reauth <- next.ExpiresAt:
	default:
		select { // replace a pending one with the newer
		case <-reauth:
		default:
		}
		reauth <- next.ExpiresAt
	}
	return true
}

func selectorString(p *Principal) string {
	if p.selector.Personal {
		return "personal"
	}
	if p.selector.OrganizationID != "" {
		return "org:" + p.selector.OrganizationID
	}
	return ""
}

// watch owns the connection's timers: the native ping, the Tick, the expiry and
// the membership recheck. It ends with the connection.
func (g *Gateway) watch(c *Conn, p *Principal, authz Authorizer, sock fwsSocket, reauth <-chan time.Time) {
	ping := time.NewTicker(g.Config.PingInterval)
	tick := time.NewTicker(g.Config.TickInterval)
	recheck := time.NewTicker(g.Config.RecheckInterval)
	expire := time.NewTimer(max(time.Until(p.ExpiresAt), 0))
	defer func() { ping.Stop(); tick.Stop(); recheck.Stop(); expire.Stop() }()
	for {
		select {
		case <-c.Done():
			return
		case <-ping.C:
			if err := sock.Ping(g.Config.PingInterval); err != nil {
				return
			}
		case <-tick.C:
			if head, err := g.heads.get(p.Scopes, g.now(), g.Log); err == nil {
				b, _ := proto.Marshal(&wire.ServerFrame{Body: &wire.ServerFrame_Tick{Tick: &wire.Tick{Head: head}}})
				c.Send(b)
			}
			g.hint(c)
		case next := <-reauth:
			if !expire.Stop() {
				select {
				case <-expire.C:
				default:
				}
			}
			expire.Reset(max(time.Until(next), 0))
		case <-expire.C:
			c.Finish(ByeFrame(ByeTokenExpired, 0), ClosePolicy, ByeTokenExpired)
			return
		case <-recheck.C:
			ctx, cancel := context.WithTimeout(context.Background(), g.Config.AuthorizeTimeout)
			ok, err := authz.Recheck(ctx, p)
			cancel()
			if err == nil && !ok {
				c.Finish(ByeFrame(ByeRevoked, 0), ClosePolicy, ByeRevoked)
				return
			}
		}
	}
}

// replayLimiter allows each principal a few replays per window.
type replayLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	seen   map[string][]time.Time
}

func newReplayLimiter(max int, window time.Duration) *replayLimiter {
	return &replayLimiter{max: max, window: window, seen: map[string][]time.Time{}}
}

func (l *replayLimiter) allow(key string, now time.Time) bool {
	if l.max <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.seen[key] = recent
		return false
	}
	l.seen[key] = append(recent, now)
	if len(l.seen) > 10000 { // bounded: forget principals with nothing recent
		for k, v := range l.seen {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= l.window {
				delete(l.seen, k)
			}
		}
	}
	return true
}

// headCache shares the head of a scope set across the connections that hold it,
// so a Tick per connection is not a table read per connection.
type headCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]cachedHead
}

type cachedHead struct {
	head    string
	expires time.Time
}

func newHeadCache(ttl time.Duration) *headCache {
	return &headCache{ttl: ttl, m: map[string]cachedHead{}}
}

func (h *headCache) get(scopes []string, now time.Time, log ChangeLog) (string, error) {
	sorted := slices.Clone(scopes)
	slices.Sort(sorted)
	key := strings.Join(sorted, "|")
	h.mu.Lock()
	if c, ok := h.m[key]; ok && now.Before(c.expires) {
		h.mu.Unlock()
		return c.head, nil
	}
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	head, err := log.Head(ctx, scopes)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	if len(h.m) > 5000 {
		clear(h.m)
	}
	h.m[key] = cachedHead{head: head, expires: now.Add(h.ttl)}
	h.mu.Unlock()
	return head, nil
}

// frameBudget is a token bucket for what one connection sends: rate a second,
// a burst of twice that. Not shared, so it needs no lock.
type frameBudget struct {
	rate   float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newFrameBudget(perSecond int, now func() time.Time) *frameBudget {
	return &frameBudget{rate: float64(perSecond), tokens: float64(2 * perSecond), last: now(), now: now}
}

func (b *frameBudget) take() bool {
	if b.rate <= 0 {
		return true
	}
	at := b.now()
	b.tokens = min(b.tokens+at.Sub(b.last).Seconds()*b.rate, 2*b.rate)
	b.last = at
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
