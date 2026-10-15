package realtime

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
)

func serveProbeApp(t *testing.T, origins []string) string {
	t.Helper()
	app := fiber.New()
	RegisterProbe(app.Group("/v1.0"), origins)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return "ws://" + ln.Addr().String() + "/v1.0/realtime/probe"
}

func TestProbeEchoesTheFirstFrame(t *testing.T) {
	url := serveProbeApp(t, nil)
	conn, _, err := (&fws.Dialer{HandshakeTimeout: 2 * time.Second}).Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(fws.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, got, err := conn.ReadMessage(); err != nil || string(got) != "hello" {
		t.Fatalf("echo = %q, %v", got, err)
	}
}

func TestProbeClosesOnAnEmptyFirstFrame(t *testing.T) {
	url := serveProbeApp(t, nil)
	conn, _, err := (&fws.Dialer{HandshakeTimeout: 2 * time.Second}).Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.WriteMessage(fws.BinaryMessage, nil)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil || !strings.Contains(err.Error(), "close") {
		t.Fatalf("an empty first frame should close the connection, got %v", err)
	}
}

func TestProbeRefusesAnUnlistedOrigin(t *testing.T) {
	url := serveProbeApp(t, []string{"https://billing.aoctech.app"})
	_, res, err := (&fws.Dialer{HandshakeTimeout: 2 * time.Second}).Dial(url, http.Header{"Origin": []string{"https://evil.example"}})
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, err = %v", res, err)
	}
	conn, _, err := (&fws.Dialer{HandshakeTimeout: 2 * time.Second}).Dial(url, http.Header{"Origin": []string{"https://billing.aoctech.app"}})
	if err != nil {
		t.Fatalf("a listed origin was refused: %v", err)
	}
	conn.Close()
}
