package server

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestTargetBatchIdleAndDeadlineReset(t *testing.T) {
	c, peer := net.Pipe()
	defer c.Close()
	defer peer.Close()
	buf := make([]byte, 256*1024)
	buf[0] = 'a'
	start := time.Now()
	n, err := fillTargetRead(c, buf, 1)
	if n != 1 || err != nil || time.Since(start) < targetReadIdleWindow-time.Millisecond {
		t.Fatalf("idle batch: %d %v %v", n, err, time.Since(start))
	}
	// A subsequent read can wait beyond the old deadline and still succeed.
	go func() { time.Sleep(2 * targetReadIdleWindow); peer.Write([]byte("b")) }()
	n, err = c.Read(buf)
	if n != 1 || err != nil || buf[0] != 'b' {
		t.Fatalf("deadline leaked: %d %v", n, err)
	}
}

func TestTargetBatchRenewsIdleOnProgress(t *testing.T) {
	c, peer := net.Pipe()
	defer c.Close()
	defer peer.Close()
	buf := make([]byte, 1024)
	buf[0] = 'a'
	go func() {
		for _, v := range []byte("bcd") {
			time.Sleep(targetReadIdleWindow / 2)
			if _, e := peer.Write([]byte{v}); e != nil {
				return
			}
		}
	}()
	start := time.Now()
	n, err := fillTargetRead(c, buf, 1)
	if err != nil || string(buf[:n]) != "abcd" || time.Since(start) < 2*targetReadIdleWindow {
		t.Fatalf("idle not renewed: %q %v %v", buf[:n], err, time.Since(start))
	}
}

func TestTargetBatchFullAndEOF(t *testing.T) {
	for _, size := range []int{17, 256 * 1024} {
		c, peer := net.Pipe()
		buf := make([]byte, 256*1024)
		buf[0] = 42
		payload := bytes.Repeat([]byte{42}, size-1)
		// Full-buffer case tests immediate return, not concurrent peer closure.
		// net.Pipe.SetReadDeadline fails after peer.Close, unlike a TCP FIN.
		go func() {
			peer.Write(payload)
			if size < len(buf) {
				peer.Close()
			}
		}()
		n, err := fillTargetRead(c, buf, 1)
		if n != size || !bytes.Equal(buf[:n], bytes.Repeat([]byte{42}, size)) {
			t.Fatalf("lost bytes: %d", n)
		}
		if size < len(buf) && err != io.EOF {
			t.Fatalf("missing EOF: %v", err)
		}
		if size == len(buf) && err != nil {
			t.Fatal(err)
		}
		c.Close()
		peer.Close()
	}
	// A full buffer never enters the wait, even with a closed connection.
	c, p := net.Pipe()
	c.Close()
	p.Close()
	if n, err := fillTargetRead(c, make([]byte, 16), 16); n != 16 || err != nil {
		t.Fatalf("full buffer waited: %d %v", n, err)
	}
}

func TestTargetBatchCloseInterruptsWait(t *testing.T) {
	c, p := net.Pipe()
	defer p.Close()
	go func() { time.Sleep(10 * time.Millisecond); c.Close() }()
	n, err := fillTargetRead(c, make([]byte, 1024), 1)
	if n != 1 || err == nil {
		t.Fatalf("close ignored: %d %v", n, err)
	}
}
