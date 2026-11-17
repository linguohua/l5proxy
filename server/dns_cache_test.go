package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testDNSQuery(id uint16, kind uint16) []byte {
	q := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 0, 0, 1}
	binary.BigEndian.PutUint16(q, id)
	binary.BigEndian.PutUint16(q[len(q)-4:], kind)
	return q
}
func testDNSAnswer(q []byte, ttl uint32) []byte {
	w := append([]byte(nil), q...)
	w[2] = 0x81
	w[3] = 0x80
	w[7] = 1
	kind := dnsWord(q, len(q)-4)
	w = append(w, 192, 12, 0, byte(kind), 0, 1, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(w[len(w)-4:], ttl)
	if kind == 1 {
		w = append(w, 0, 4, 203, 0, 113, 1)
	} else {
		w = append(w, 0, 16)
		w = append(w, make([]byte, 16)...)
	}
	return w
}
func TestDNSCacheTTLCaseIsolation(t *testing.T) {
	c := newDNSCache()
	now := time.Now()
	c.now = func() time.Time { return now }
	q := testDNSQuery(1, 1)
	_, err := c.query(q, "a", func() ([]byte, error) { return testDNSAnswer(q, 3), nil })
	if err != nil {
		t.Fatal(err)
	}
	q2 := testDNSQuery(2, 1)
	q2[13] = 'W'
	now = now.Add(2 * time.Second)
	w, err := c.query(q2, "a", func() ([]byte, error) { t.Fatal("cache miss"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if dnsWord(w, 0) != 2 || w[13] != 'W' || binary.BigEndian.Uint32(w[len(q)+6:]) != 1 {
		t.Fatal("incorrect rewritten response")
	}
	now = now.Add(time.Second)
	calls := 0
	for _, up := range []string{"a", "b"} {
		c.query(q, up, func() ([]byte, error) { calls++; return testDNSAnswer(q, 30), nil })
	}
	q6 := testDNSQuery(3, 28)
	c.query(q6, "a", func() ([]byte, error) { calls++; return testDNSAnswer(q6, 30), nil })
	if calls != 3 {
		t.Fatal(calls)
	}
}
func TestDNSCacheConcurrentIDsAndRetry(t *testing.T) {
	c := newDNSCache()
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(id uint16) {
			defer wg.Done()
			q := testDNSQuery(id, 1)
			w, e := c.query(q, "a", func() ([]byte, error) {
				calls.Add(1)
				time.Sleep(30 * time.Millisecond)
				return testDNSAnswer(q, 30), nil
			})
			if e != nil || dnsWord(w, 0) != id {
				t.Errorf("id %d: %v", id, e)
			}
		}(uint16(i))
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	q := testDNSQuery(1, 1)
	_, e := c.query(q, "error", func() ([]byte, error) { return nil, errors.New("failed") })
	if e == nil {
		t.Fatal("missing error")
	}
	_, e = c.query(q, "error", func() ([]byte, error) { return testDNSAnswer(q, 10), nil })
	if e != nil {
		t.Fatal(e)
	}
}
func TestDNSCacheBypassNegativeAndMalformed(t *testing.T) {
	q := testDNSQuery(1, 1)
	for _, change := range []func([]byte){func(w []byte) { w[2] |= 2 }, func(w []byte) { w[3] |= 3 }, func(w []byte) { w[11] = 1 }, func(w []byte) { w[3] |= 32 }, func(w []byte) { binary.BigEndian.PutUint32(w[len(q)+6:], 0) }} {
		c := newDNSCache()
		calls := 0
		for i := 0; i < 2; i++ {
			_, e := c.query(q, "a", func() ([]byte, error) { calls++; w := testDNSAnswer(q, 10); change(w); return w, nil })
			if e != nil {
				t.Fatal(e)
			}
		}
		if calls != 2 || len(c.entries) != 0 {
			t.Fatal("cached unsupported response")
		}
	}
	for _, change := range []func([]byte){func(q []byte) { q[11] = 1 }, func(q []byte) { q[3] = 16 }, func(q []byte) { q[5] = 2 }} {
		special := append([]byte(nil), q...)
		change(special)
		if _, ok := dnsEligible(special); ok {
			t.Fatal("special query eligible")
		}
	}
	w := testDNSAnswer(q, 30)
	w[13] = 'x'
	if dnsValidate(q, w) == nil {
		t.Fatal("question mismatch accepted")
	}
	w = testDNSAnswer(q, 30)
	w[len(q)] = 192
	w[len(q)+1] = byte(len(q))
	if dnsCacheTTLs(q, w) != nil {
		t.Fatal("pointer loop accepted")
	}
}
func TestDNSCacheCNAME(t *testing.T) {
	q := testDNSQuery(1, 1)
	w := append([]byte(nil), q...)
	w[2] = 0x81
	w[3] = 0x80
	w[7] = 2
	w = append(w, 192, 12, 0, 5, 0, 1, 0, 0, 0, 3, 0, 8)
	alias := len(w)
	w = append(w, 5, 'a', 'l', 'i', 'a', 's', 192, 16)
	w = append(w, 192, byte(alias), 0, 1, 0, 1, 0, 0, 0, 30, 0, 4, 203, 0, 113, 1)
	ttls := dnsCacheTTLs(q, w)
	if len(ttls) != 2 || ttls[0].value != 3 {
		t.Fatal(ttls)
	}
}
func TestDNSExchangeLargeAndMismatchedDatagram(t *testing.T) {
	server, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	q := testDNSQuery(123, 1)
	q[11] = 1 // EDNS-like additional section: bypass cache
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 65535)
		_, addr, err := server.ReadFromUDP(buf)
		if err != nil {
			done <- err
			return
		}
		reply := testDNSAnswer(q, 30)
		reply = append(reply, bytes.Repeat([]byte{42}, 2000)...)
		wrong := append([]byte(nil), reply...)
		wrong[0] ^= 1
		server.WriteToUDP(wrong, addr)
		_, err = server.WriteToUDP(reply, addr)
		done <- err
	}()
	w, e := exchangeDNS(q, server.LocalAddr().(*net.UDPAddr))
	if e != nil {
		t.Fatal(e)
	}
	if len(w) <= 2000 || dnsWord(w, 0) != 123 {
		t.Fatal("truncated or wrong response")
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
