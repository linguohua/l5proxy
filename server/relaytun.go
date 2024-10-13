package server

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	websocketWriteDealine = 5
)

type RelayTunnel struct {
	id int

	conn      *websocket.Conn
	connRelay *websocket.Conn

	writeLock      sync.Mutex
	writeLockRelay sync.Mutex
}

func newRelayTunnel(cc *TunnelCreateCtx) (*RelayTunnel, error) {
	uu := fmt.Sprintf("%s?endpoint=%s&uuid=%s&", cc.relayURL, cc.endpoint, cc.account)
	d := websocket.Dialer{
		ReadBufferSize:   wsReadBufSize,
		WriteBufferSize:  wsWriteBufSize,
		HandshakeTimeout: 5 * time.Second,
	}

	relayConn, _, err := d.Dial(uu, nil)
	if err != nil {

		return nil, err
	}

	rt := &RelayTunnel{
		id: cc.id,

		conn:      cc.conn,
		connRelay: relayConn,
	}

	cc.conn.SetPingHandler(func(data string) error {
		rt.writeRelayPing([]byte(data))
		return nil
	})

	cc.conn.SetPongHandler(func(data string) error {
		rt.writeRelayPong([]byte(data))
		return nil
	})

	relayConn.SetPingHandler(func(data string) error {
		rt.writePing([]byte(data))
		return nil
	})

	relayConn.SetPongHandler(func(data string) error {
		rt.writePong([]byte(data))
		return nil
	})

	return rt, nil
}

func (t *RelayTunnel) idx() int {
	return t.id
}

func (t *RelayTunnel) writeRelayPing(msg []byte) error {
	t.writeLockRelay.Lock()
	t.connRelay.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.connRelay.WriteMessage(websocket.PingMessage, msg)
	t.writeLockRelay.Unlock()

	return err
}

func (t *RelayTunnel) writeRelayPong(msg []byte) error {
	t.writeLockRelay.Lock()
	t.connRelay.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.connRelay.WriteMessage(websocket.PongMessage, msg)
	t.writeLockRelay.Unlock()

	return err
}

func (t *RelayTunnel) writePing(msg []byte) error {
	t.writeLock.Lock()
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.conn.WriteMessage(websocket.PingMessage, msg)
	t.writeLock.Unlock()

	return err
}

func (t *RelayTunnel) writePong(msg []byte) error {
	t.writeLock.Lock()
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.conn.WriteMessage(websocket.PongMessage, msg)
	t.writeLock.Unlock()

	return err
}

func (t *RelayTunnel) write(msg []byte) error {
	t.writeLock.Lock()
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.conn.WriteMessage(websocket.BinaryMessage, msg)
	t.writeLock.Unlock()

	return err
}

func (t *RelayTunnel) writeRelay(msg []byte) error {
	t.writeLockRelay.Lock()
	t.connRelay.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.connRelay.WriteMessage(websocket.BinaryMessage, msg)
	t.writeLockRelay.Unlock()

	return err
}

func (rt *RelayTunnel) serve() {
	go rt.serveRelay()

	c := rt.conn
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Errorf("Relay tunnel %d [south %s] read failed:%s", rt.id, c.RemoteAddr().String(), err)
			break
		}

		// log.Infof("Tunnel recv message, len:%d", len(message))
		err = rt.onTunnelMessage(message)
		if err != nil {
			log.Errorf("Relay tunnel %d [south %s] onTunnelMessage failed:%s", rt.id, c.RemoteAddr().String(), err)
			break
		}
	}

	rt.onClose()
}

func (rt *RelayTunnel) serveRelay() {
	c := rt.connRelay
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Errorf("Relay tunnel %d [north %s] read failed:%s", rt.id, c.RemoteAddr().String(), err)
			break
		}

		// log.Infof("Tunnel recv message, len:%d", len(message))
		err = rt.onTunnelMessageRelay(message)
		if err != nil {
			log.Errorf("Relay tunnel %d [north %s] onTunnelMessage failed:%s", rt.id, c.RemoteAddr().String(), err)
			break
		}
	}

	rt.onCloseRelay()
}

func (rt *RelayTunnel) onClose() {
	rt.writeLockRelay.Lock()
	log.Infof("Relay tunnel %d south closed, now close north", rt.id)
	rt.connRelay.Close()
	rt.writeLockRelay.Unlock()
}

func (rt *RelayTunnel) onCloseRelay() {
	rt.writeLock.Lock()
	log.Infof("Relay tunnel %d north closed, now close south", rt.id)
	rt.conn.Close()
	rt.writeLock.Unlock()
}

func (rt *RelayTunnel) onTunnelMessage(message []byte) error {
	cmd := message[0]
	if cmd == cMDReqDataExt {
		// add my timestamp
		rt.setTimestamp(message)
	}

	// write to relay target server
	return rt.writeRelay(message)
}

func (rt *RelayTunnel) onTunnelMessageRelay(message []byte) error {
	cmd := message[0]
	if cmd == cMDReqDataExt {
		// add my timestamp
		rt.setTimestamp(message)
	}

	// write to client
	return rt.write(message)
}

func (rt *RelayTunnel) keepalive() {
	// nothing to do
}

func (rt *RelayTunnel) rateLimitReset(int) {
	// nothing to do
}

func (rt *RelayTunnel) setTimestamp(message []byte) {
	extraBytesLen := 8 + 4*2
	size := len(message)

	if size <= 5+extraBytesLen {
		log.Errorf("Relay tunnel %d setTimestamp failed, size %d not enough", rt.id, size)
		return
	}

	offset := size - extraBytesLen

	unixMilli := binary.LittleEndian.Uint64(message[offset:])

	unixMilliNow := time.Now().UnixMilli()
	if unixMilliNow < int64(unixMilli) {
		log.Errorf("Relay tunnel %d setTimestamp failed, unix time not large than prev's", rt.id)
		return
	}

	elapsedMilli := uint16(unixMilliNow - int64(unixMilli))
	offset = offset + 8

	add := false
	for i := 0; i < 4; i++ {
		ts := binary.LittleEndian.Uint16(message[offset:])
		if ts == 0 {
			binary.LittleEndian.PutUint16(message[offset:], elapsedMilli)
			add = true
			break
		}

		offset = offset + 2
	}

	if !add {
		log.Errorf("Relay tunnel %d setTimestamp failed, slots are fulled", rt.id)
	}
}
