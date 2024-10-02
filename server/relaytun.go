package server

import (
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

func newRelayTunnel(idx int, conn *websocket.Conn, endpoint string,
	account string, url2 string) (*RelayTunnel, error) {
	uu := fmt.Sprintf("%s?endpoint=%s&uuid=%s&", url2, endpoint, account)
	relayConn, _, err := websocket.DefaultDialer.Dial(uu, nil)
	if err != nil {

		return nil, err
	}

	rt := &RelayTunnel{
		id: idx,

		conn:      conn,
		connRelay: relayConn,
	}

	conn.SetPingHandler(func(data string) error {
		rt.writeRelayPing([]byte(data))
		return nil
	})

	conn.SetPongHandler(func(data string) error {
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
			log.Errorf("Tunnel read failed:%s", err)
			break
		}

		// log.Infof("Tunnel recv message, len:%d", len(message))
		err = rt.onTunnelMessage(message)
		if err != nil {
			log.Errorf("Tunnel onTunnelMessage failed:%s", err)
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
			log.Errorf("Tunnel read failed:%s", err)
			break
		}

		// log.Infof("Tunnel recv message, len:%d", len(message))
		err = rt.onTunnelMessageRelay(message)
		if err != nil {
			log.Errorf("Tunnel onTunnelMessage failed:%s", err)
			break
		}
	}

	rt.onCloseRelay()
}

func (rt *RelayTunnel) onClose() {
	rt.writeLockRelay.Lock()
	rt.connRelay.Close()
	rt.writeLockRelay.Unlock()
}

func (rt *RelayTunnel) onCloseRelay() {
	rt.writeLock.Lock()
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

func (rt *RelayTunnel) rateLimitReset(uint) {
	// nothing to do
}
