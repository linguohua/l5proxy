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

	relayIndex int
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
		rt.onTunnelPing([]byte(data))
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
		rt.onRelayPong([]byte(data))
		return nil
	})

	return rt, nil
}

func (t *RelayTunnel) idx() int {
	return t.id
}

func (t *RelayTunnel) onTunnelPing(data []byte) {
	defer func() {
		t.writeRelayPing([]byte(data))
	}()

	if len(data) == 0 || data[0] == 0 {
		return
	}

	t.relayIndex = int(data[0])

	if len(data) != 1+t.relayIndex*8 {
		return
	}

	newData := make([]byte, len(data)+8)

	// add index
	newData[0] = data[0] + 1

	copy(newData[1:], data[1:])
	now := time.Now().UnixMilli()
	binary.LittleEndian.PutUint64(newData[len(data):], uint64(now))

	data = newData
}

func (t *RelayTunnel) onRelayPong(data []byte) {
	defer func() {
		t.writePong([]byte(data))
	}()

	if len(data) == 0 || data[0] == 0 {
		return
	}

	if t.relayIndex == 0 {
		return
	}

	allRelay := int(data[0]) - 1
	if t.relayIndex == allRelay {
		// we are the last relay, need to gernerate timestamp reply packet
		newData := make([]byte, 2*allRelay+len(data))
		newData[0] = data[0]
		copy(newData[1+2*allRelay:], data[1:])
		data = newData
	}

	if len(data) != 1+allRelay*2+(1+t.relayIndex)*8 {
		return
	}

	myoffset := 1 + 2*allRelay + t.relayIndex*8
	unixMilli := binary.LittleEndian.Uint64(data[myoffset:])
	unixMilliNow := time.Now().UnixMilli()

	elapsedMilli := uint16(unixMilliNow - int64(unixMilli))
	binary.LittleEndian.PutUint16(data[1+(allRelay-t.relayIndex)*2:], elapsedMilli)

	data = data[0:myoffset]
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
	// write to relay target server
	return rt.writeRelay(message)
}

func (rt *RelayTunnel) onTunnelMessageRelay(message []byte) error {
	// write to client
	return rt.write(message)
}

func (rt *RelayTunnel) keepalive() {
	// nothing to do
}

func (rt *RelayTunnel) rateLimitReset(int) {
	// nothing to do
}
