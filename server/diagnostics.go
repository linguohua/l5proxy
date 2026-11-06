package server

// Explicitly enabled, event-driven diagnostics on a mode-0600 Unix socket only.
// Never registered on the public HTTP/WebSocket listener.
import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const diagnosticHeader = "X-L5-Diagnostic-Id"

type tunnelDiagnostic struct {
	id      string
	created int64
	asleep  atomic.Bool
	closed  atomic.Bool
	pingTX  atomic.Uint64
	pingRX  atomic.Uint64
	pongTX  atomic.Uint64
	pongRX  atomic.Uint64
	sleepRX atomic.Uint64
	awakeRX atomic.Uint64
	lastTX  atomic.Int64
	lastRX  atomic.Int64
}

var diagnostics = struct {
	sync.Mutex
	enabled atomic.Bool
	next    atomic.Uint64
	epoch   string
	active  map[string]*tunnelDiagnostic
	close   map[string]func() error
	history []*tunnelDiagnostic
}{epoch: fmt.Sprintf("%x", time.Now().UnixNano()), active: make(map[string]*tunnelDiagnostic), close: make(map[string]func() error)}

func nextDiagnosticID() string {
	if !diagnostics.enabled.Load() {
		return ""
	}
	return fmt.Sprintf("%s-%d", diagnostics.epoch, diagnostics.next.Add(1))
}
func newTunnelDiagnostic(id string, asleep bool, close func() error) *tunnelDiagnostic {
	if id == "" {
		return nil
	}
	d := &tunnelDiagnostic{id: id, created: time.Now().UnixMilli()}
	d.asleep.Store(asleep)
	diagnostics.Lock()
	diagnostics.active[id] = d
	diagnostics.close[id] = close
	diagnostics.Unlock()
	return d
}
func (d *tunnelDiagnostic) event(kind string) {
	if d == nil {
		return
	}
	switch kind {
	case "ping-tx":
		d.pingTX.Add(1)
	case "ping-rx":
		d.pingRX.Add(1)
	case "pong-tx":
		d.pongTX.Add(1)
	case "pong-rx":
		d.pongRX.Add(1)
	case "sleep":
		d.sleepRX.Add(1)
		d.asleep.Store(true)
	case "awake":
		d.awakeRX.Add(1)
		d.asleep.Store(false)
	case "tx":
		d.lastTX.Store(time.Now().UnixMilli())
	case "rx":
		d.lastRX.Store(time.Now().UnixMilli())
	}
}
func (d *tunnelDiagnostic) finish() {
	if d == nil || d.closed.Swap(true) {
		return
	}
	diagnostics.Lock()
	defer diagnostics.Unlock()
	delete(diagnostics.active, d.id)
	delete(diagnostics.close, d.id)
	if len(diagnostics.history) == 128 {
		diagnostics.history = diagnostics.history[1:]
	}
	diagnostics.history = append(diagnostics.history, d)
}
func (d *tunnelDiagnostic) snapshot() map[string]any {
	return map[string]any{"id": d.id, "created_unix_ms": d.created, "asleep": d.asleep.Load(), "closed": d.closed.Load(),
		"ping_tx": d.pingTX.Load(), "ping_rx": d.pingRX.Load(), "pong_tx": d.pongTX.Load(), "pong_rx": d.pongRX.Load(),
		"sleep_rx": d.sleepRX.Load(), "awake_rx": d.awakeRX.Load(), "last_tx_unix_ms": d.lastTX.Load(), "last_rx_unix_ms": d.lastRX.Load()}
}
func diagnosticHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/snapshot" && r.Method == http.MethodGet {
		diagnostics.Lock()
		records := append([]*tunnelDiagnostic{}, diagnostics.history...)
		for _, d := range diagnostics.active {
			records = append(records, d)
		}
		diagnostics.Unlock()
		sort.Slice(records, func(i, j int) bool { return records[i].id < records[j].id })
		result := make([]map[string]any, 0, len(records))
		for _, d := range records {
			result = append(result, d.snapshot())
		}
		json.NewEncoder(w).Encode(map[string]any{"now_unix_ms": time.Now().UnixMilli(), "tunnels": result})
		return
	}
	if r.URL.Path == "/close" && r.Method == http.MethodPost {
		var request struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&request) != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		diagnostics.Lock()
		close := diagnostics.close[request.ID]
		diagnostics.Unlock()
		if close == nil {
			http.Error(w, "tunnel not found", 404)
			return
		}
		if close() != nil {
			http.Error(w, "close failed", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"closed_id": request.ID})
		return
	}
	http.NotFound(w, r)
}
func StartDiagnostics() (func(), error) {
	path := os.Getenv("L5_DIAGNOSTICS_SOCKET")
	if path == "" {
		return func() {}, nil
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("diagnostic path exists and is not a socket")
		}
		// Do not unlink another live instance's diagnostic socket.
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			conn.Close()
			return nil, fmt.Errorf("diagnostic socket already in use")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	diagnostics.enabled.Store(true)
	srv := &http.Server{Handler: http.HandlerFunc(diagnosticHandler), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(listener)
	return func() { srv.Close(); diagnostics.enabled.Store(false) }, nil
}
