package server

import (
	"net"

	log "github.com/sirupsen/logrus"
)

// Request request
type Request struct {
	isUsed bool
	idx    uint16
	tag    uint16
	t      *Tunnel

	conn *net.TCPConn
}

func newRequest(t *Tunnel, idx uint16) *Request {
	r := &Request{t: t, idx: idx}

	return r
}

func (r *Request) dofree() {
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
	}
}

func (r *Request) onClientFinished() {
	if r.conn != nil {
		r.conn.CloseWrite()
	}
}

func (r *Request) onClientData(data []byte) {
	if r.conn != nil {
		err := writeAll(data, r.conn)
		if err != nil {
			log.Println("onClientData, write failed:", err)
		} //else {
		// log.Println("onClientData, write:", len(data))
		//}
	}
}

func (r *Request) proxy() {
	c := r.conn
	if c == nil {
		return
	}

	if !r.isUsed {
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)

		if !r.isUsed {
			// request is free!
			log.Println("proxy read, request is free, discard data:", n)
			break
		}

		if err != nil {
			// log.Println("proxy read failed:", err)
			r.t.onRequestTerminate(r)
			break
		}

		if n == 0 {
			// log.Println("proxy read, server half close")
			r.t.onRequestHalfClosed(r)
			break
		}

		r.t.onRequestData(r, buf[:n])
	}
}

func writeAll(buf []byte, nc net.Conn) error {
	wrote := 0
	l := len(buf)
	for {
		n, err := nc.Write(buf[wrote:])
		if err != nil {
			return err
		}

		wrote = wrote + n
		if wrote == l {
			break
		}
	}

	return nil
}
