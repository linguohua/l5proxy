package server

import (
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// AccountConfig config
type AccountConfig struct {
	uuid           string
	rateLimit      uint // 0: no limit
	maxTunnelCount uint
}

// Account account
type Account struct {
	uuid    string
	tunnels map[int]*Tunnel
	tidx    int

	rateLimit      uint // 0: no limit
	maxTunnelCount uint
}

func newAccount(uc *AccountConfig) *Account {
	return &Account{
		uuid:           uc.uuid,
		rateLimit:      uc.rateLimit,
		maxTunnelCount: uc.maxTunnelCount,
		tunnels:        make(map[int]*Tunnel),
	}
}

func (a *Account) acceptWebsocket(conn *websocket.Conn) {
	log.Printf("account:%s accept websocket, total:%d", a.uuid, 1+len(a.tunnels))

	if a.maxTunnelCount > 0 && uint(len(a.tunnels)) >= a.maxTunnelCount {
		conn.Close()
		return
	}

	idx := a.tidx
	a.tidx++

	tun := newTunnel(idx, conn, 200, a.rateLimit)
	a.tunnels[idx] = tun
	defer delete(a.tunnels, idx)

	tun.serve()
}

func (a *Account) keepalive() {
	for _, t := range a.tunnels {
		t.keepalive()
	}
}

func (a *Account) rateLimitReset() {
	if a.rateLimit < 1 {
		return
	}

	for _, t := range a.tunnels {
		t.rateLimitReset(a.rateLimit)
	}
}
