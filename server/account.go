package server

import (
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// Account account
type Account struct {
	uuid    string
	tunnels map[int]*Tunnel
	tidx    int
}

func newAccount(uuid string) *Account {
	return &Account{
		uuid:    uuid,
		tunnels: make(map[int]*Tunnel),
	}
}

func (a *Account) acceptWebsocket(conn *websocket.Conn) {
	log.Printf("account:%s accept websocket, total:%d", a.uuid, 1+len(a.tunnels))

	idx := a.tidx
	a.tidx++

	tun := newTunnel(idx, conn, 200)
	a.tunnels[idx] = tun
	defer delete(a.tunnels, idx)

	tun.serve()
}

func (a *Account) keepalive() {
	for _, t := range a.tunnels {
		t.keepalive()
	}
}
