// Command rtprobe dials a realtime WebSocket endpoint, sends one frame, then
// holds the connection open answering pings, and reports whether the whole path
// (Cloudflare, load balancer, HAProxy, nginx, the app) carried it.
//
//	go run ./cmd/rtprobe -url wss://billing-api.aoctech.app/v1.0/realtime/probe -origin https://billing.aoctech.app
//
// It is also the base for the load tool in docs/plans/2026-10-14-realtime-r5.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	fws "github.com/fasthttp/websocket"
)

func main() {
	url := flag.String("url", "wss://billing-api.aoctech.app/v1.0/realtime/probe", "WebSocket URL")
	origin := flag.String("origin", "https://billing.aoctech.app", "Origin header (must be an allowed origin)")
	hold := flag.Duration("hold", 4*time.Minute, "how long to keep the connection open")
	flag.Parse()
	os.Exit(run(*url, *origin, *hold))
}

func run(url, origin string, hold time.Duration) int {
	dialer := fws.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, res, err := dialer.Dial(url, http.Header{"Origin": []string{origin}})
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		fmt.Printf("upgrade: FAILED status=%d err=%v\n", status, err)
		return 1
	}
	defer conn.Close()
	fmt.Printf("upgrade: %d\n", res.StatusCode)

	payload := []byte("probe")
	start := time.Now()
	if err := conn.WriteMessage(fws.BinaryMessage, payload); err != nil {
		fmt.Println("first frame: write failed:", err)
		return 1
	}
	if _, echo, err := conn.ReadMessage(); err != nil || string(echo) != string(payload) {
		fmt.Printf("first echo: FAILED (%v)\n", err)
		return 1
	}
	fmt.Printf("first echo: %s\n", time.Since(start).Round(time.Millisecond))

	// The default ping handler answers the server's native pings with a pong, as
	// long as something is reading.
	pings := 0
	conn.SetPingHandler(func(data string) error {
		pings++
		return conn.WriteControl(fws.PongMessage, []byte(data), time.Now().Add(5*time.Second))
	})
	deadline := time.Now().Add(hold)
	conn.SetReadDeadline(deadline.Add(10 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		held := time.Since(start).Round(time.Second)
		if err == nil {
			continue
		}
		var closeErr *fws.CloseError
		switch {
		case errors.As(err, &closeErr):
			fmt.Printf("closed after %s: code=%d text=%q pings=%d\n", held, closeErr.Code, closeErr.Text, pings)
		default:
			fmt.Printf("closed after %s: %v pings=%d\n", held, err, pings)
		}
		if time.Now().Before(deadline.Add(-5 * time.Second)) {
			fmt.Println("RESULT: dropped early; the path did not hold the connection")
			return 1
		}
		fmt.Println("RESULT: held for the whole period")
		return 0
	}
}
