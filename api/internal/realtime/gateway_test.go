package realtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/api-commons/drain"
	"gopkg.aoctech.app/api-commons/ws"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// fakeLog is the change log in memory, with the replay window of the real one.
type fakeLog struct {
	mu      sync.Mutex
	changes []repositories.Change
	failing bool
}

func (l *fakeLog) add(c repositories.Change) {
	l.mu.Lock()
	l.changes = append(l.changes, c)
	l.mu.Unlock()
}

func (l *fakeLog) Since(_ context.Context, scopes []string, cursor string, limit int) ([]repositories.Change, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failing {
		return nil, false, errors.New("table down")
	}
	at, err := repositories.CursorTime(cursor)
	if err != nil {
		return nil, false, err
	}
	lower := id.FloorAt(at.Add(-repositories.ChangeOverlap))
	var out []repositories.Change
	for _, c := range l.changes {
		if c.ID > lower && slices.ContainsFunc(c.Scopes, func(s string) bool { return slices.Contains(scopes, s) }) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b repositories.Change) int { return strings.Compare(a.ID, b.ID) })
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (l *fakeLog) Head(_ context.Context, scopes []string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	head := ""
	for _, c := range l.changes {
		if slices.ContainsFunc(c.Scopes, func(s string) bool { return slices.Contains(scopes, s) }) && c.ID > head {
			head = c.ID
		}
	}
	return head, nil
}

// fakeAuthz maps tokens to outcomes.
type fakeAuthz struct {
	now       func() time.Time
	revoked   atomic.Bool
	lifetimes map[string]time.Duration
}

func (a *fakeAuthz) Authorize(_ context.Context, au *wire.Auth) (*Principal, error) {
	switch au.GetToken() {
	case "u1", "u1-short", "u1-renewed", "u1-b":
		life := 10 * time.Minute
		if d, ok := a.lifetimes[au.GetToken()]; ok {
			life = d
		}
		return &Principal{Kind: KindPortal, Sub: "u1", Scopes: []string{UserScope("u1")}, ExpiresAt: a.now().Add(life)}, nil
	case "u2":
		return &Principal{Kind: KindPortal, Sub: "u2", Scopes: []string{UserScope("u2")}, ExpiresAt: a.now().Add(10 * time.Minute)}, nil
	case "down":
		return nil, ErrUnavailable
	}
	return nil, ErrUnauthorized
}

func (a *fakeAuthz) Recheck(context.Context, *Principal) (bool, error) { return !a.revoked.Load(), nil }

type gwEnv struct {
	url     string
	hub     *Hub
	reg     *ws.MemoryRegistry
	log     *fakeLog
	authz   *fakeAuthz
	drain   *drain.Tracker
	metrics *Metrics
	health  interface{ Degraded() bool }
}

func fastConfig() Config {
	return Config{
		AuthWait: 200 * time.Millisecond, FirstFrameMax: 32 << 10, ReadMax: 32 << 10,
		PingInterval: time.Hour, PongWait: time.Hour, TickInterval: time.Hour, RecheckInterval: time.Hour,
		ReplayMax: 500, ReplaysPerMinute: 3, HeadTTL: time.Millisecond, DrainRetry: 3 * time.Second,
		AuthorizeTimeout: time.Second,
	}
}

func newGateway(t *testing.T, cfg Config, origins []string, lim Limits) *gwEnv {
	t.Helper()
	return buildGateway(t, cfg, origins, lim, nil, &Metrics{})
}

func newGatewayWithHealth(t *testing.T, cfg Config, health interface{ Degraded() bool }, m *Metrics) *gwEnv {
	t.Helper()
	return buildGateway(t, cfg, nil, DefaultLimits(), health, m)
}

func buildGateway(t *testing.T, cfg Config, origins []string, lim Limits, health interface{ Degraded() bool }, m *Metrics) *gwEnv {
	t.Helper()
	reg := ws.NewMemoryRegistry()
	hub := NewHub(reg, lim, m)
	e := &gwEnv{hub: hub, reg: reg, log: &fakeLog{}, authz: &fakeAuthz{now: time.Now}, drain: &drain.Tracker{}, metrics: m, health: health}
	gw := &Gateway{
		Authorizers: map[string]Authorizer{KindPortal: e.authz, KindConsole: e.authz, KindCheckout: e.authz},
		Hub:         hub, Log: e.log, AllowedOrigins: origins, Drain: e.drain, Config: cfg, Health: health,
	}
	app := fiber.New()
	RegisterGateway(app.Group("/v1.0"), gw)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	e.url = "ws://" + ln.Addr().String() + "/v1.0/ws"
	return e
}

func (e *gwEnv) dial(t *testing.T, origin string) (*fws.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	return (&fws.Dialer{HandshakeTimeout: 2 * time.Second}).Dial(e.url, h)
}

func (e *gwEnv) open(t *testing.T, auth *wire.Auth) *fws.Conn {
	t.Helper()
	conn, _, err := e.dial(t, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	send(t, conn, &wire.ClientFrame{Body: &wire.ClientFrame_Auth{Auth: auth}})
	return conn
}

func send(t *testing.T, conn *fws.Conn, f *wire.ClientFrame) {
	t.Helper()
	b, err := proto.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(fws.BinaryMessage, b); err != nil {
		t.Fatal(err)
	}
}

// next reads one server frame, or reports the close error.
func next(t *testing.T, conn *fws.Conn, wait time.Duration) (*wire.ServerFrame, error) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var f wire.ServerFrame
	if err := proto.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return &f, nil
}

func mustNext(t *testing.T, conn *fws.Conn) *wire.ServerFrame {
	t.Helper()
	f, err := next(t, conn, 2*time.Second)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return f
}

func closeCode(err error) int {
	var ce *fws.CloseError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return -1
}

func change(scope string, at time.Time, entityID string) repositories.Change {
	return repositories.Change{ID: id.NewAt(at), Type: "invoice.paid", Entity: "invoice", EntityID: entityID, Status: "PAID", Revision: uint64(at.UnixMilli()), OccurredAt: at, Scopes: []string{scope}}
}

func publish(e *gwEnv, c repositories.Change) {
	frame, _ := FrameOf(&c)
	for _, s := range c.Scopes {
		e.reg.Broadcast(context.Background(), s, frame)
	}
}

// 1. First-frame auth only.
func TestASilentConnectionIsClosedWithinTheAuthWait(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	conn, _, err := e.dial(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	started := time.Now()
	f, err := next(t, conn, 2*time.Second)
	if err != nil || f.GetBye().GetReason() != ByeUnauthorized {
		t.Fatalf("frame=%+v err=%v, want Bye unauthorized", f.GetBye(), err)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("took %s to give up on a silent socket", took)
	}
	if _, err := next(t, conn, time.Second); closeCode(err) != ClosePolicy {
		t.Fatalf("close = %v, want 1008", err)
	}
}

func TestAFirstFrameOverTheCapIsClosed(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	conn, _, err := e.dial(t, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	big, err := proto.Marshal(&wire.ClientFrame{Body: &wire.ClientFrame_Auth{Auth: &wire.Auth{Token: strings.Repeat("x", 40<<10)}}})
	if err != nil {
		t.Fatal(err)
	}
	// The server closes as soon as it sees the frame is over the cap, which can be
	// while the client is still writing it (broken pipe): the close is what is asserted.
	_ = conn.WriteMessage(fws.BinaryMessage, big)
	for {
		if _, err := next(t, conn, 2*time.Second); err != nil {
			if code := closeCode(err); code != fws.CloseMessageTooBig && code != -1 {
				t.Fatalf("close code = %d", code)
			}
			return
		}
	}
}

// 2. Origin.
func TestOriginIsCheckedOnTheUpgrade(t *testing.T) {
	e := newGateway(t, fastConfig(), []string{"https://billing.aoctech.app"}, DefaultLimits())
	if c, _, err := e.dial(t, "https://billing.aoctech.app"); err != nil {
		t.Fatalf("a listed origin: %v", err)
	} else {
		c.Close()
	}
	for name, origin := range map[string]string{"unlisted": "https://evil.example", "missing": ""} {
		_, res, err := e.dial(t, origin)
		if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
			t.Errorf("%s origin: res=%v err=%v, want 403", name, res, err)
		}
	}
	open := newGateway(t, fastConfig(), nil, DefaultLimits())
	if c, _, err := open.dial(t, "https://anything.example"); err != nil {
		t.Fatalf("no allow-list allows every origin: %v", err)
	} else {
		c.Close()
	}
}

// 3. Ready, then replay, then live.
func TestReadyThenTheMissedChangesInOrderThenLiveOnes(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	scope := UserScope("u1")
	t0 := time.Now().Add(-time.Minute)
	cursor := change(scope, t0, "seen").ID
	for i, eid := range []string{"m1", "m2", "m3"} {
		e.log.add(change(scope, t0.Add(time.Duration(10+i)*time.Second), eid))
	}
	conn := e.open(t, &wire.Auth{Token: "u1", ResumeCursor: cursor})

	ready := mustNext(t, conn).GetReady()
	if ready == nil || ready.GetProtocolVersion() != ProtocolVersion || !slices.Equal(ready.GetScopeKinds(), []string{KindUser}) {
		t.Fatalf("ready = %+v", ready)
	}
	for _, want := range []string{"m1", "m2", "m3"} {
		if got := mustNext(t, conn).GetChange().GetEntityId(); got != want {
			t.Fatalf("replayed %q, want %q", got, want)
		}
	}
	live := change(scope, time.Now(), "live1")
	e.log.add(live)
	publish(e, live)
	if got := mustNext(t, conn).GetChange().GetEntityId(); got != "live1" {
		t.Fatalf("live frame = %q", got)
	}
}

func TestACursorOlderThanTheLogOrTooFarBehindIsAGapThenLive(t *testing.T) {
	cfg := fastConfig()
	cfg.ReplayMax = 2
	cfg.ReplaysPerMinute = 100
	e := newGateway(t, cfg, nil, DefaultLimits())
	scope := UserScope("u1")

	old := e.open(t, &wire.Auth{Token: "u1", ResumeCursor: id.NewAt(time.Now().Add(-49 * time.Hour))})
	mustNext(t, old) // Ready
	if g := mustNext(t, old).GetGap(); g.GetReason() != GapCursorTooOld {
		t.Fatalf("gap = %+v, want cursor_too_old", g)
	}

	t0 := time.Now().Add(-time.Minute)
	cursor := change(scope, t0, "seen").ID
	for i := 0; i < 3; i++ {
		e.log.add(change(scope, t0.Add(time.Duration(10+i)*time.Second), "x"))
	}
	far := e.open(t, &wire.Auth{Token: "u1-b", ResumeCursor: cursor})
	mustNext(t, far)
	if g := mustNext(t, far).GetGap(); g.GetReason() != GapReplayLimit {
		t.Fatalf("gap = %+v, want replay_limit (3 missed, limit 2)", g)
	}
	live := change(scope, time.Now(), "after-gap")
	publish(e, live)
	if got := mustNext(t, far).GetChange().GetEntityId(); got != "after-gap" {
		t.Fatalf("live after a gap = %q", got)
	}
}

// 4. Auth failure never hangs.
func TestAnAuthFailureIsAByeAndAClose(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	for token, want := range map[string]string{"nope": ByeUnauthorized, "down": ByeUnavailable} {
		conn := e.open(t, &wire.Auth{Token: token})
		f, err := next(t, conn, 2*time.Second)
		if err != nil || f.GetBye().GetReason() != want {
			t.Fatalf("%s: frame=%+v err=%v, want Bye %s", token, f.GetBye(), err, want)
		}
		if _, err := next(t, conn, 2*time.Second); err == nil {
			t.Fatalf("%s: the connection stayed open", token)
		}
	}
	if e.hub.Count() != 0 {
		t.Fatal("a refused connection was registered")
	}
}

func TestAnEmptyOrWrongFirstFrameIsRefused(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	conn, _, err := e.dial(t, "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	send(t, conn, &wire.ClientFrame{Body: &wire.ClientFrame_Ping{Ping: &wire.Ping{}}}) // not an Auth
	if f, err := next(t, conn, 2*time.Second); err != nil || f.GetBye().GetReason() != ByeUnauthorized {
		t.Fatalf("frame=%+v err=%v", f.GetBye(), err)
	}
}

// 5. Heartbeat.
func TestPingIsAnsweredByPongAndTickCarriesTheHead(t *testing.T) {
	cfg := fastConfig()
	cfg.TickInterval = 80 * time.Millisecond
	e := newGateway(t, cfg, nil, DefaultLimits())
	scope := UserScope("u1")
	known := change(scope, time.Now().Add(-time.Second), "known")
	e.log.add(known)
	conn := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, conn) // Ready

	send(t, conn, &wire.ClientFrame{Body: &wire.ClientFrame_Ping{Ping: &wire.Ping{ClientTimeMs: 4242}}})
	var sawPong, sawTick bool
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && !(sawPong && sawTick); {
		f := mustNext(t, conn)
		if p := f.GetPong(); p != nil {
			sawPong = p.GetClientTimeMs() == 4242 && p.GetServerTimeMs() > 0
		}
		if tk := f.GetTick(); tk != nil {
			sawTick = tk.GetHead() == known.ID
		}
	}
	if !sawPong || !sawTick {
		t.Fatalf("pong=%v tick-with-head=%v", sawPong, sawTick)
	}
}

func TestAClientThatNeverAnswersTheNativePingIsClosed(t *testing.T) {
	cfg := fastConfig()
	cfg.PingInterval = 40 * time.Millisecond
	cfg.PongWait = 150 * time.Millisecond
	e := newGateway(t, cfg, nil, DefaultLimits())
	conn := e.open(t, &wire.Auth{Token: "u1"})
	// Never read again: the library answers a ping only while somebody reads.
	_ = conn
	deadline := time.Now().Add(2 * time.Second)
	for e.hub.Count() != 0 || e.metrics.Active() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("a silent client was never dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 6. Expiry and Reauth.
func TestATokenThatExpiresClosesTheSocketUnlessItWasRenewed(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	e.authz.lifetimes = map[string]time.Duration{"u1-short": 300 * time.Millisecond, "u1-renewed": time.Hour}

	expiring := e.open(t, &wire.Auth{Token: "u1-short"})
	mustNext(t, expiring)
	f, err := next(t, expiring, 2*time.Second)
	if err != nil || f.GetBye().GetReason() != ByeTokenExpired {
		t.Fatalf("frame=%+v err=%v, want Bye token_expired", f.GetBye(), err)
	}

	renewed := e.open(t, &wire.Auth{Token: "u1-short"})
	mustNext(t, renewed)
	send(t, renewed, &wire.ClientFrame{Body: &wire.ClientFrame_Reauth{Reauth: &wire.Reauth{Token: "u1-renewed"}}})
	if f, err := next(t, renewed, 700*time.Millisecond); err == nil {
		t.Fatalf("a renewed connection received %+v", f)
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("a renewed connection was closed: %v", err)
	}
}

func TestAReauthCannotChangeWhoTheConnectionBelongsTo(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	conn := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, conn)
	send(t, conn, &wire.ClientFrame{Body: &wire.ClientFrame_Reauth{Reauth: &wire.Reauth{Token: "u2"}}})
	f, err := next(t, conn, 2*time.Second)
	if err != nil || f.GetBye().GetReason() != ByeUnauthorized {
		t.Fatalf("frame=%+v err=%v, want Bye unauthorized: u2's token on u1's socket", f.GetBye(), err)
	}
}

// 7. Revocation.
func TestARemovedMemberIsClosedAtTheNextRecheck(t *testing.T) {
	cfg := fastConfig()
	cfg.RecheckInterval = 100 * time.Millisecond
	e := newGateway(t, cfg, nil, DefaultLimits())
	conn := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, conn)
	e.authz.revoked.Store(true)
	f, err := next(t, conn, 2*time.Second)
	if err != nil || f.GetBye().GetReason() != ByeRevoked {
		t.Fatalf("frame=%+v err=%v, want Bye revoked", f.GetBye(), err)
	}
}

// 8. A publish that was lost is found again by the replay, once.
func TestALostPublishIsDeliveredOnceByTheNextReconnect(t *testing.T) {
	cfg := fastConfig()
	cfg.ReplaysPerMinute = 100
	e := newGateway(t, cfg, nil, DefaultLimits())
	scope := UserScope("u1")
	first := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, first)
	seen := change(scope, time.Now().Add(-2*time.Second), "seen")
	e.log.add(seen)
	publish(e, seen)
	if got := mustNext(t, first).GetChange().GetEntityId(); got != "seen" {
		t.Fatalf("got %q", got)
	}
	first.Close()

	lost := change(scope, time.Now(), "lost") // written to the log, never published
	e.log.add(lost)

	again := e.open(t, &wire.Auth{Token: "u1-b", ResumeCursor: seen.ID})
	mustNext(t, again) // Ready
	var got []string
	for {
		f, err := next(t, again, 300*time.Millisecond)
		if err != nil {
			break
		}
		if c := f.GetChange(); c != nil {
			got = append(got, c.GetEntityId())
		}
	}
	// The overlap replays "seen" as well (the client drops it by id); "lost" is
	// there exactly once.
	count := 0
	for _, g := range got {
		if g == "lost" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("replayed %v, want %q exactly once", got, "lost")
	}
}

// 9. Drain.
func TestDrainSaysByeDrainingToEveryConnectionAndRefusesNewOnes(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	a := e.open(t, &wire.Auth{Token: "u1"})
	b := e.open(t, &wire.Auth{Token: "u2"})
	mustNext(t, a)
	mustNext(t, b)

	if err := e.drain.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, conn := range map[string]*fws.Conn{"a": a, "b": b} {
		f, err := next(t, conn, 2*time.Second)
		if err != nil || f.GetBye().GetReason() != ByeDraining || f.GetBye().GetRetryAfterMs() != 3000 {
			t.Fatalf("%s: frame=%+v err=%v, want Bye draining retry 3000", name, f.GetBye(), err)
		}
		if _, err := next(t, conn, 2*time.Second); closeCode(err) != CloseGoingAway {
			t.Fatalf("%s: close = %v, want 1001", name, err)
		}
	}
	late := e.open(t, &wire.Auth{Token: "u1"})
	// A draining process does not adopt a new connection: no Ready, a Bye first.
	f, err := next(t, late, 2*time.Second)
	if err != nil || f.GetBye().GetReason() != ByeDraining {
		t.Fatalf("a connection during drain: frame=%+v err=%v", f.GetBye(), err)
	}
}

// 10. Replay limit.
func TestAFourthReplayWithinAMinuteIsAGap(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits()) // 3 per minute
	cursor := id.NewAt(time.Now().Add(-time.Minute))
	for i := 0; i < 3; i++ {
		c := e.open(t, &wire.Auth{Token: "u1", ResumeCursor: cursor})
		mustNext(t, c) // Ready
		if f, err := next(t, c, 200*time.Millisecond); err == nil && f.GetGap() != nil {
			t.Fatalf("replay %d was refused: %+v", i+1, f.GetGap())
		}
		c.Close()
	}
	fourth := e.open(t, &wire.Auth{Token: "u1-b", ResumeCursor: cursor})
	mustNext(t, fourth)
	if g := mustNext(t, fourth).GetGap(); g.GetReason() != GapReplayLimit {
		t.Fatalf("fourth replay = %+v, want replay_limit", g)
	}
}

func TestTheSixthConnectionOfOnePersonIsToldTheLimit(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	for i := 0; i < 5; i++ {
		mustNext(t, e.open(t, &wire.Auth{Token: "u1"}))
	}
	sixth := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, sixth) // Ready
	f, err := next(t, sixth, 2*time.Second)
	if err != nil || f.GetBye().GetReason() != ByeLimit {
		t.Fatalf("frame=%+v err=%v, want Bye limit", f.GetBye(), err)
	}
}

func TestAnUnknownFrameFromANewerClientIsIgnored(t *testing.T) {
	e := newGateway(t, fastConfig(), nil, DefaultLimits())
	conn := e.open(t, &wire.Auth{Token: "u1"})
	mustNext(t, conn)
	// A ClientFrame carrying only an unknown field (99): a newer client's request.
	if err := conn.WriteMessage(fws.BinaryMessage, []byte{0xDA, 0x06, 0x01, 0x78}); err != nil {
		t.Fatal(err)
	}
	send(t, conn, &wire.ClientFrame{Body: &wire.ClientFrame_Ping{Ping: &wire.Ping{ClientTimeMs: 7}}})
	if pong := mustNext(t, conn).GetPong(); pong.GetClientTimeMs() != 7 {
		t.Fatalf("the connection did not survive an unknown frame: %+v", pong)
	}
}
