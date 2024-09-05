package server

import (
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// AccountConfig config
type AccountConfig struct {
	UUID          string `json:"UUID"`
	RateLimit     uint   `json:"rateLimit"` // 0: no limit
	MaxTunnel     uint   `json:"maxTunnel"`
	UseWhitelistA bool   `json:"useWhitelistA"`
}

// Account account
type Account struct {
	uuid          string
	useWhitelistA bool
	tunnels       map[int]*Tunnel
	tidx          int

	rateLimit uint // 0: no limit
	maxTunnel uint
}

func newAccount(uc *AccountConfig) *Account {
	return &Account{
		uuid:          uc.UUID,
		rateLimit:     uc.RateLimit,
		maxTunnel:     uc.MaxTunnel,
		useWhitelistA: uc.UseWhitelistA,
		tunnels:       make(map[int]*Tunnel),
	}
}

func (a *Account) acceptWebsocket(conn *websocket.Conn, udpServ *UDPServ, endpoint string) {
	log.Printf("account:%s accept websocket, total:%d", a.uuid, 1+len(a.tunnels))

	if a.maxTunnel > 0 && uint(len(a.tunnels)) >= a.maxTunnel {
		conn.Close()
		return
	}

	idx := a.tidx
	a.tidx++

	tun := newTunnel(idx, conn, 200, a.rateLimit, endpoint, udpServ)
	// tun.reverseServ = a.reverseServ
	a.tunnels[idx] = tun
	defer delete(a.tunnels, idx)

	tun.serve()
}

func (a *Account) keepalive() {
	for _, t := range a.tunnels {
		t.keepalive()
	}

	for _, t := range a.tunnels {
		t.cache.keepalive()
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
