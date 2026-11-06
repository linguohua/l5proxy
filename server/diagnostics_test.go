package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiagnosticsPrivateSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix deployment socket")
	}
	path := filepath.Join(t.TempDir(), "control.sock")
	t.Setenv("L5_DIAGNOSTICS_SOCKET", path)
	stop, err := StartDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("incorrect socket permissions")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	rsp, err := client.Get("http://localhost/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	rsp.Body.Close()
	if rsp.StatusCode != 200 {
		t.Fatal("snapshot unavailable")
	}
	if second, err := StartDiagnostics(); err == nil {
		second()
		t.Fatal("must not replace live socket")
	}
}
