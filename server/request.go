package server

import (
	"net"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	tcpSocketWriteDeadline = 5
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
		r.conn.SetWriteDeadline(time.Now().Add(tcpSocketWriteDeadline * time.Second))
		err := writeAll(data, r.conn)
		if err != nil {
			log.Errorf("onClientData, write failed:%s", err)
		} //else {
		// log.Infof("onClientData, write bytes length:%d", len(data))
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
			log.Debug("proxy read, request is free, discard data:", n)
			break
		}

		if err != nil {
			// log.Debug("proxy read failed:", err)
			r.t.onRequestTerminate(r)
			break
		}

		if n == 0 {
			// log.Debug("proxy read, server half close")
			r.t.onRequestHalfClosed(r)
			break
		}

		err = r.t.onRequestData(r, buf[:n])
		if err != nil {
			log.Errorf("proxy read, tunnel error: %s", err)
			break
		}
	}
}

func writeAll(buf []byte, nc net.Conn) error {
	wrote := 0
	l := len(buf)
	for {
		nc.SetWriteDeadline(time.Now().Add(tcpSocketWriteDeadline * time.Second))
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
