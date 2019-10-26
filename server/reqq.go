package server

import (
	"fmt"
)

// Reqq request queue
type Reqq struct {
	owner *Tunnel
	array []*Request
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

func (q *Reqq) free(idx uint16, tag uint16) error {
	if idx >= uint16(len(q.array)) {
		return fmt.Errorf("free, idx %d >= len %d", idx, uint16(len(q.array)))
	}

	req := q.array[idx]
	if !req.isUsed {
		return fmt.Errorf("free, req %d:%d is in not used", idx, tag)
	}

	if req.tag != tag {
		return fmt.Errorf("free, req %d:%d is in not match tag %d", idx, tag, req.tag)
	}

	// log.Printf("reqq free req %d:%d", idx, tag)

	req.dofree()
	req.tag++
	req.isUsed = false

	return nil
}

func (q *Reqq) get(idx uint16, tag uint16) (*Request, error) {
	if idx >= uint16(len(q.array)) {
		return nil, fmt.Errorf("get, idx %d >= len %d", idx, uint16(len(q.array)))
	}

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
	for _, r := range q.array {
		if r.isUsed {
			q.free(r.idx, r.tag)
		}
	}
}
