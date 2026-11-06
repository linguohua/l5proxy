package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatNegotiationAndCompatibility(t *testing.T) {
	for _, adaptive := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "adaptive"}[adaptive], func(t *testing.T) {
			oldAccounts, oldReverse := accountMap, reverseServer
			account := &Account{uuid: "heartbeat-test", tunnels: make(map[int]ITunnel)}
			accountMap = map[string]*Account{"heartbeat-test": account}
			reverseServer = &ReverseServ{tunnels: make(map[string]TunnelMap)}
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				wsHandler(w, r)
			}))
			defer func() { srv.Close(); accountMap, reverseServer = oldAccounts, oldReverse }()
			headers := http.Header{}
			if adaptive {
				headers.Set(heartbeatHeader, heartbeatCapability)
			}
			client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?uuid=heartbeat-test&endpoint=test", headers)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { client.Close(); <-done }()
			if got := response.Header.Get(heartbeatHeader); (got == heartbeatCapability) != adaptive {
				t.Fatalf("negotiated %q", got)
			}
			var tunnel *Tunnel
			until := time.Now().Add(time.Second)
			for tunnel == nil && time.Now().Before(until) {
				account.writeLock.Lock()
				for _, v := range account.tunnels {
					tunnel = v.(*Tunnel)
				}
				account.writeLock.Unlock()
				if tunnel == nil {
					time.Sleep(time.Millisecond)
				}
			}
			if tunnel == nil {
				t.Fatal("tunnel not registered")
			}
			if tunnel.clientHeartbeat != adaptive {
				t.Fatal("negotiated mode not passed to tunnel")
			}
			runs := 1
			if adaptive {
				runs = 6
			}
			for i := 0; i < runs; i++ {
				tunnel.keepalive()
			}
			pings := 0
			pongReceived := false
			client.SetPingHandler(func(data string) error {
				pings++
				return client.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
			})
			payload := []byte("client-probe-123")
			client.SetPongHandler(func(data string) error {
				if !bytes.Equal([]byte(data), payload) {
					t.Errorf("wrong Pong payload %q", data)
				}
				pongReceived = true
				// Stop ReadMessage without waiting for business data.
				return http.ErrAbortHandler
			})
			if err := client.WriteControl(websocket.PingMessage, payload, time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			client.SetReadDeadline(time.Now().Add(2 * time.Second))
			client.ReadMessage()
			if !pongReceived {
				t.Fatal("client probe was not answered")
			}
			if adaptive && pings != 0 {
				t.Fatalf("server sent %d redundant Pings", pings)
			}
			if !adaptive && pings != 1 {
				t.Fatalf("legacy heartbeat missing: %d", pings)
			}
		})
	}
}

func TestScreenHeartbeatSleepResumeAndInterval(t *testing.T) {
	diagnostics.enabled.Store(true)
	defer diagnostics.enabled.Store(false)

	for _, initiallyAsleep := range []bool{false, true} {
		t.Run(fmt.Sprint(initiallyAsleep), func(t *testing.T) {
			oldAccounts, oldReverse := accountMap, reverseServer
			account := &Account{uuid: "screen-test", tunnels: make(map[int]ITunnel)}
			accountMap = map[string]*Account{"screen-test": account}
			reverseServer = &ReverseServ{tunnels: make(map[string]TunnelMap)}
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); wsHandler(w, r) }))
			defer func() { srv.Close(); accountMap, reverseServer = oldAccounts, oldReverse }()
			screen := "1"
			if initiallyAsleep {
				screen = "0"
			}
			headers := http.Header{heartbeatHeader: []string{screenHeartbeatCapability}, serverPingHeader: []string{"1800"}, screenHeader: []string{screen}}
			client, rsp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?uuid=screen-test&endpoint=test", headers)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { client.Close(); <-done }()
			if rsp.Header.Get(heartbeatHeader) != screenHeartbeatCapability || rsp.Header.Get(serverPingHeader) != "1800" {
				t.Fatal("v2 not acknowledged")
			}
			var tunnel *Tunnel
			for until := time.Now().Add(time.Second); tunnel == nil && time.Now().Before(until); {
				account.writeLock.Lock()
				for _, v := range account.tunnels {
					tunnel = v.(*Tunnel)
				}
				account.writeLock.Unlock()
				if tunnel == nil {
					time.Sleep(time.Millisecond)
				}
			}
			if tunnel == nil {
				t.Fatal("missing tunnel")
			}
			pings := make(chan string, 8)
			pongs := make(chan string, 8)
			client.SetPingHandler(func(data string) error {
				pings <- data
				return client.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
			})
			client.SetPongHandler(func(data string) error { pongs <- data; return nil })
			readerDone := make(chan struct{})
			go func() {
				defer close(readerDone)
				for {
					if _, _, err := client.ReadMessage(); err != nil {
						return
					}
				}
			}()
			defer func() { client.Close(); <-readerDone }()
			exchange := func(payload string) {
				t.Helper()
				if err := client.WriteControl(websocket.PingMessage, []byte(payload), time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-pongs:
					if got != payload {
						t.Fatal("Pong mismatch")
					}
				case <-time.After(time.Second):
					t.Fatal("Pong missing")
				}
			}
			tunnel.lastTx.Store(time.Now().Add(-29 * time.Minute).UnixNano())
			tunnel.screenKeepalive(time.Now())
			select {
			case <-pings:
				t.Fatal("Ping before 30m")
			default:
			}
			exchange("l5:sleep")
			tunnel.lastTx.Store(time.Now().Add(-2 * time.Hour).UnixNano())
			tunnel.screenKeepalive(time.Now())
			// An ordered control exchange confirms the reader passed all earlier frames.
			exchange("sleep-barrier")
			if tunnel.diagnostic == nil {
				t.Fatal("missing diagnostic record")
			}
			d := tunnel.diagnostic.snapshot()
			if d["sleep_rx"] != uint64(1) || d["asleep"] != true || d["ping_tx"] != uint64(0) {
				t.Fatalf("wrong sleep counters: %v", d)
			}
			if rsp.Header.Get(diagnosticHeader) != tunnel.diagnostic.id {
				t.Fatal("diagnostic ID mismatch")
			}

			select {
			case <-pings:
				t.Fatal("Ping while sleeping")
			default:
			}
			exchange("l5:awake")
			tunnel.lastTx.Store(time.Now().Add(-31 * time.Minute).UnixNano())
			tunnel.screenKeepalive(time.Now())
			select {
			case <-pings:
			case <-time.After(time.Second):
				t.Fatal("missing 30m Ping after resume")
			}
			exchange("awake-barrier")
			d = tunnel.diagnostic.snapshot()
			if d["awake_rx"] != uint64(1) || d["asleep"] != false || d["ping_tx"] != uint64(1) {
				t.Fatalf("wrong awake counters: %v", d)
			}
			response := httptest.NewRecorder()
			diagnosticHandler(response, httptest.NewRequest("POST", "/close", strings.NewReader(`{"id":"`+tunnel.diagnostic.id+`"}`)))
			if response.Code != 200 {
				t.Fatal("targeted diagnostic close failed")
			}

			tunnel.screenKeepalive(time.Now())
		})
	}
}

// Opt-in deployment smoke test; reads credentials only on the server and never logs them.
func TestDeployedHeartbeatAndTCP(t *testing.T) {
	path := os.Getenv("L5_SMOKE_CONFIG")
	if path == "" {
		t.Skip("deployment smoke test is opt-in")
	}
	cfg, err := ParseConfig(path)
	if err != nil {
		t.Fatal("cannot load smoke configuration")
	}
	var uuid string
	for _, account := range cfg.Accounts {
		if account.RelayURL == "" {
			uuid = account.UUID
			break
		}
	}
	if uuid == "" {
		t.Fatal("no direct account for smoke test")
	}
	_, port, err := net.SplitHostPort(cfg.Server.Address)
	if err != nil {
		t.Fatal("invalid server address")
	}
	endpoint := url.URL{Scheme: "ws", Host: net.JoinHostPort("127.0.0.1", port), Path: cfg.Server.WebsocketPath}
	q := url.Values{"uuid": []string{uuid}, "endpoint": []string{"power-smoke-test"}}
	endpoint.RawQuery = q.Encode()
	dialer := *websocket.DefaultDialer
	if host := os.Getenv("L5_SMOKE_PROXY_HOST"); host != "" {
		endpoint.Scheme = "wss"
		endpoint.Host = net.JoinHostPort(host, "443")
		dialer.Proxy = nil
		dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:443")
		}
	}
	for _, mode := range []string{"legacy", "awake", "asleep"} {
		t.Run(mode, func(t *testing.T) {
			headers := http.Header{}
			if mode != "legacy" {
				headers.Set(heartbeatHeader, screenHeartbeatCapability)
				headers.Set(serverPingHeader, "1800")
				screen := "1"
				if mode == "asleep" {
					screen = "0"
				}
				headers.Set(screenHeader, screen)
			}
			client, rsp, err := dialer.Dial(endpoint.String(), headers)
			if err != nil {
				t.Fatal("deployed websocket dial failed")
			}
			defer client.Close()
			if mode != "legacy" && (rsp.Header.Get(heartbeatHeader) != screenHeartbeatCapability || rsp.Header.Get(serverPingHeader) != "1800") {
				t.Fatal("deployed v2 negotiation failed")
			}
			if mode == "legacy" && rsp.Header.Get(heartbeatHeader) != "" {
				t.Fatal("legacy negotiation changed")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					defer conn.Close()
					io.Copy(conn, conn)
				}
			}()
			if mode == "asleep" && os.Getenv("L5_SMOKE_IDLE") != "" {
				duration, err := time.ParseDuration(os.Getenv("L5_SMOKE_IDLE"))
				if err != nil || duration > 2*time.Minute {
					t.Fatal("invalid smoke idle duration")
				}
				time.Sleep(duration)
			}
			pongCount := 0
			client.SetPongHandler(func(data string) error { pongCount++; return nil })
			for _, payload := range []string{"l5:sleep", "l5:awake", "smoke-probe"} {
				if err := client.WriteControl(websocket.PingMessage, []byte(payload), time.Now().Add(3*time.Second)); err != nil {
					t.Fatal("control write failed")
				}
			}
			created := []byte{2, 0, 0, 1, 0, 0, 127, 0, 0, 1, 0, 0}
			binary.LittleEndian.PutUint16(created[10:], uint16(listener.Addr().(*net.TCPAddr).Port))
			if err := client.WriteMessage(websocket.BinaryMessage, created); err != nil {
				t.Fatal("create request failed")
			}
			payload := []byte("power-policy-tcp-roundtrip")
			if err := client.WriteMessage(websocket.BinaryMessage, append([]byte{1, 0, 0, 1, 0}, payload...)); err != nil {
				t.Fatal("request data write failed")
			}
			client.SetReadDeadline(time.Now().Add(5 * time.Second))
			var received []byte
			for len(received) < len(payload) {
				_, frame, err := client.ReadMessage()
				if err != nil {
					t.Fatal("TCP echo response failed")
				}
				if len(frame) < 5 || frame[0] != 1 {
					t.Fatal("unexpected TCP response")
				}
				received = append(received, frame[5:]...)
			}
			if !bytes.Equal(received, payload) || pongCount != 3 {
				t.Fatal("TCP/Pong roundtrip mismatch")
			}
			client.WriteMessage(websocket.BinaryMessage, []byte{3, 0, 0, 1, 0})
		})
	}
}
