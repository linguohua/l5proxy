package server

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

type dnsTTL struct {
	offset int
	value  uint32
}
type dnsEntry struct {
	wire     []byte
	at       time.Time
	ttl      []dnsTTL
	lifetime uint32
}
type dnsFlight struct {
	done  chan struct{}
	entry *dnsEntry
	err   error
}
type dnsCache struct {
	mu        sync.Mutex
	entries   map[string]*dnsEntry
	pending   map[string]*dnsFlight
	lastSweep time.Time
	now       func() time.Time
}

func newDNSCache() *dnsCache {
	return &dnsCache{entries: make(map[string]*dnsEntry), pending: make(map[string]*dnsFlight), now: time.Now}
}
func (e *dnsEntry) live(now time.Time) bool {
	return now.Sub(e.at) < time.Duration(e.lifetime)*time.Second
}
func (e *dnsEntry) render(q []byte, now time.Time) []byte {
	w := append([]byte(nil), e.wire...)
	copy(w[:2], q[:2])
	// Only eligible, uncompressed questions are coalesced. Preserve caller case.
	copy(w[12:len(q)], q[12:])
	age := uint64(max(0, now.Sub(e.at)/time.Second))
	for _, t := range e.ttl {
		remaining := uint32(0)
		if age < uint64(t.value) {
			remaining = t.value - uint32(age)
		}
		binary.BigEndian.PutUint32(w[t.offset:], remaining)
	}
	return w
}
func (c *dnsCache) query(q []byte, upstream string, fetch func() ([]byte, error)) ([]byte, error) {
	canonical, ok := dnsEligible(q)
	if !ok {
		w, e := fetch()
		if e == nil {
			e = dnsValidate(q, w)
		}
		return w, e
	}
	key := upstream + "\x00" + string(q[2:4]) + canonical
	c.mu.Lock()
	now := c.now()
	// Opportunistic cleanup only; no periodic goroutine or wakeup timer.
	if c.lastSweep.IsZero() || now.Sub(c.lastSweep) >= time.Minute {
		for k, e := range c.entries {
			if !e.live(now) {
				delete(c.entries, k)
			}
		}
		c.lastSweep = now
	}
	if e := c.entries[key]; e != nil {
		if e.live(now) {
			c.mu.Unlock()
			return e.render(q, now), nil
		}
		delete(c.entries, key)
	}
	if f := c.pending[key]; f != nil {
		c.mu.Unlock()
		<-f.done
		if f.err != nil {
			return nil, f.err
		}
		return f.entry.render(q, c.now()), nil
	}
	f := &dnsFlight{done: make(chan struct{})}
	c.pending[key] = f
	c.mu.Unlock()
	// Always release followers, including an unexpected panic in the fetch path.
	defer func() {
		if p := recover(); p != nil {
			c.mu.Lock()
			f.err = fmt.Errorf("DNS upstream panic: %v", p)
			delete(c.pending, key)
			close(f.done)
			c.mu.Unlock()
			panic(p)
		}
	}()
	w, err := fetch()
	if err == nil {
		err = dnsValidate(q, w)
	}
	if err == nil {
		_, end, ok := dnsQuestion(w, 12, false)
		if !ok || end != len(q) {
			err = errors.New("unsupported DNS response question encoding")
		}
	}
	var entry *dnsEntry
	if err == nil {
		entry = &dnsEntry{wire: append([]byte(nil), w...), at: c.now()}
		entry.ttl = dnsCacheTTLs(q, w)
		for _, t := range entry.ttl {
			if entry.lifetime == 0 || t.value < entry.lifetime {
				entry.lifetime = t.value
			}
		}
	}
	c.mu.Lock()
	f.entry = entry
	f.err = err
	if err == nil && entry.lifetime > 0 {
		c.entries[key] = entry
	}
	delete(c.pending, key)
	close(f.done)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return entry.render(q, c.now()), nil
}
func dnsWord(w []byte, p int) uint16 { return binary.BigEndian.Uint16(w[p : p+2]) }
func dnsName(w []byte, start int, compression bool) (string, int, bool) {
	p, end := start, -1
	out := make([]byte, 0, 64)
	for steps := 0; steps < 128; steps++ {
		if p >= len(w) {
			return "", 0, false
		}
		n := int(w[p])
		if n&192 == 192 {
			if !compression || p+1 >= len(w) {
				return "", 0, false
			}
			target := (n&63)<<8 | int(w[p+1])
			if target < 12 || target >= p {
				return "", 0, false
			}
			if end < 0 {
				end = p + 2
			}
			p = target
			continue
		}
		if n > 63 {
			return "", 0, false
		}
		p++
		out = append(out, byte(n))
		if n == 0 {
			if end < 0 {
				end = p
			}
			return string(out), end, true
		}
		if p+n > len(w) || len(out)+n > 254 {
			return "", 0, false
		}
		for _, b := range w[p : p+n] {
			if b > 127 {
				return "", 0, false
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			out = append(out, b)
		}
		p += n
	}
	return "", 0, false
}
func dnsQuestion(w []byte, p int, compression bool) (string, int, bool) {
	n, end, ok := dnsName(w, p, compression)
	if !ok || end+4 > len(w) {
		return "", 0, false
	}
	return n + string(w[end:end+4]), end + 4, true
}
func dnsEligible(q []byte) (string, bool) {
	if len(q) < 12 || dnsWord(q, 2)&^uint16(0x0100) != 0 || dnsWord(q, 4) != 1 || dnsWord(q, 6) != 0 || dnsWord(q, 8) != 0 || dnsWord(q, 10) != 0 {
		return "", false
	}
	n, end, ok := dnsQuestion(q, 12, false)
	if !ok || end != len(q) {
		return "", false
	}
	kind := dnsWord(q, end-4)
	return n, (kind == 1 || kind == 28) && dnsWord(q, end-2) == 1
}
func dnsValidate(q, w []byte) error {
	if len(q) < 12 || len(w) < 12 || dnsWord(q, 0) != dnsWord(w, 0) || w[2]&128 == 0 || q[2]&0x78 != w[2]&0x78 || dnsWord(q, 4) != dnsWord(w, 4) {
		return errors.New("DNS response header mismatch")
	}
	a, b := 12, 12
	for i := 0; i < int(dnsWord(q, 4)); i++ {
		qa, ae, ok := dnsQuestion(q, a, true)
		wb, be, ok2 := dnsQuestion(w, b, true)
		if !ok || !ok2 || qa != wb {
			return errors.New("DNS response question mismatch")
		}
		a, b = ae, be
	}
	return nil
}
func dnsCacheTTLs(q, w []byte) []dnsTTL {
	if len(w) > 512 || dnsWord(w, 2)&^uint16(0x8580) != 0 || w[2]&1 != q[2]&1 || dnsWord(w, 6) == 0 || dnsWord(w, 8) != 0 || dnsWord(w, 10) != 0 {
		return nil
	}
	question, pos, ok := dnsQuestion(w, 12, false)
	want, eligible := dnsEligible(q)
	if !ok || !eligible || pos != len(q) || question != want {
		return nil
	}
	root, _, _ := dnsName(q, 12, false)
	qtype := dnsWord(q, len(q)-4)
	aliases := map[string]string{}
	addresses := []string{}
	ttls := []dnsTTL{}
	for i := 0; i < int(dnsWord(w, 6)); i++ {
		owner, end, ok := dnsName(w, pos, true)
		if !ok || end+10 > len(w) {
			return nil
		}
		pos = end
		kind := dnsWord(w, pos)
		ttl := binary.BigEndian.Uint32(w[pos+4:])
		size := int(dnsWord(w, pos+8))
		data := pos + 10
		end = data + size
		if dnsWord(w, pos+2) != 1 || ttl == 0 || ttl > 0x7fffffff || end > len(w) {
			return nil
		}
		switch {
		case kind == 5:
			alias, used, ok := dnsName(w, data, true)
			if !ok || used != end {
				return nil
			}
			if _, exists := aliases[owner]; exists {
				return nil
			}
			aliases[owner] = alias
		case kind == 1 && qtype == 1 && size == 4, kind == 28 && qtype == 28 && size == 16:
			addresses = append(addresses, owner)
		default:
			return nil
		}
		ttls = append(ttls, dnsTTL{pos + 4, ttl})
		pos = end
	}
	if pos != len(w) || len(addresses) == 0 {
		return nil
	}
	current := root
	seen := map[string]bool{}
	for {
		next, exists := aliases[current]
		if !exists {
			break
		}
		if seen[current] {
			return nil
		}
		seen[current] = true
		for _, a := range addresses {
			if a == current {
				return nil
			}
		}
		delete(aliases, current)
		current = next
	}
	if len(aliases) > 0 {
		return nil
	}
	for _, a := range addresses {
		if a != current {
			return nil
		}
	}
	return ttls
}
