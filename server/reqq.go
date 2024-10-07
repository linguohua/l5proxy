package server

import (
	"fmt"
	"net"
	"sync"
)

// Reqq request queue
type Reqq struct {
	owner *Tunnel
	array []*Request
	l     sync.Mutex
}

func newReqq(cap int, t *Tunnel) *Reqq {
	reqq := &Reqq{owner: t}

	reqq.array = make([]*Request, cap)
	for i := 0; i < cap; i++ {
		reqq.array[i] = newRequest(t, uint16(i))
	}

	return reqq
}

func (q *Reqq) alloc(idx uint16, tag uint16) (*Request, error) {
	q.l.Lock()
	defer q.l.Unlock()

	if idx >= uint16(len(q.array)) {
		return nil, fmt.Errorf("alloc, idx %d >= len %d", idx, uint16(len(q.array)))
	}

	req := q.array[idx]
	if req.isUsed {
		return nil, fmt.Errorf("alloc, req %d:%d is in used", idx, tag)
	}

	req.tag = tag
	req.isUsed = true

	return req, nil
}

func (q *Reqq) allocForConn(conn *net.TCPConn) (*Request, error) {
	q.l.Lock()
	defer q.l.Unlock()

	for _, req := range q.array {
		if !req.isUsed {
			req.conn = conn
			req.isUsed = true
			req.tag = req.tag + 1
			return req, nil
		}
	}
	return nil, nil
}

func (q *Reqq) free(idx uint16, tag uint16) error {
	if idx >= uint16(len(q.array)) {
		return fmt.Errorf("free, idx %d >= len %d", idx, uint16(len(q.array)))
	}

	q.l.Lock()
	defer q.l.Unlock()

	return q.freeInternal(idx, tag)
}

func (q *Reqq) freeInternal(idx uint16, tag uint16) error {
	// log.Infof("reqq free req %d:%d", idx, tag)

	req := q.array[idx]
	if !req.isUsed {
		return fmt.Errorf("free, req %d:%d is in not used", idx, tag)
	}

	if req.tag != tag {
		return fmt.Errorf("free, req %d:%d is in not match tag %d", idx, tag, req.tag)
	}

	req.dofree()
	req.tag++
	req.isUsed = false

	return nil
}

func (q *Reqq) get(idx uint16, tag uint16) (*Request, error) {
	if idx >= uint16(len(q.array)) {
		return nil, fmt.Errorf("get, idx %d >= len %d", idx, uint16(len(q.array)))
	}

	q.l.Lock()
	defer q.l.Unlock()

	req := q.array[idx]
	if !req.isUsed {
		return nil, fmt.Errorf("get, req %d:%d is not in used", idx, tag)
	}

	if req.tag != tag {
		return nil, fmt.Errorf("get, req %d:%d tag not match %d", idx, req.tag, tag)
	}

	return req, nil
}

func (q *Reqq) cleanup() {
	q.l.Lock()
	defer q.l.Unlock()

	for _, r := range q.array {
		if r.isUsed {
			q.freeInternal(r.idx, r.tag)
		}
	}
}
