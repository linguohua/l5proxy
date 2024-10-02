package server

import (
	"sync"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// AccountConfig config
type AccountConfig struct {
	UUID      string `json:"uuid"`
	RateLimit uint   `json:"rateLimit"` // 0: no limit
	MaxTunnel uint   `json:"maxTunnel"`
	RelayURL  string `json:"relayURL"`
}

type ITunnel interface {
	serve()
	keepalive()
	rateLimitReset(uint)
}

// Account account
type Account struct {
	uuid    string
	tunnels map[int]ITunnel
	tidx    int

	rateLimit uint // 0: no limit
	maxTunnel uint

	relayURL string

	writeLock sync.Mutex
}

func newAccount(uc *AccountConfig) *Account {
	return &Account{
		uuid:      uc.UUID,
		rateLimit: uc.RateLimit,
		maxTunnel: uc.MaxTunnel,
		relayURL:  uc.RelayURL,
		tunnels:   make(map[int]ITunnel),
	}
}

func (a *Account) acceptWebsocket(conn *websocket.Conn, reverseServ *ReverseServ, endpoint string) {
	log.Infof("account:%s try to accept websocket, endpoint:%s", a.uuid, endpoint)

	if a.maxTunnel > 0 && uint(len(a.tunnels)) >= a.maxTunnel {
		conn.Close()
		return
	}

	a.writeLock.Lock()
	idx := a.tidx
	a.tidx++
	a.writeLock.Unlock()

	var tun ITunnel
	if len(a.relayURL) > 0 {
		// in relay-model
		tun = newRelayTunnel(idx, conn, endpoint, a.uuid, a.relayURL)
	} else {
		tun = newTunnel(idx, conn, 200, a.rateLimit, endpoint, reverseServ)
	}

	if tun == nil {
		return
	}

	// tun.reverseServ = a.reverseServ
	a.writeLock.Lock()
	a.tunnels[idx] = tun
	log.Infof("account:%s accept websocket, total:%d", a.uuid, 1+len(a.tunnels))
	a.writeLock.Unlock()

	defer func() {
		a.writeLock.Lock()
		delete(a.tunnels, idx)
		a.writeLock.Unlock()
	}()

	tun.serve()
}

func (a *Account) keepalive() {
	a.writeLock.Lock()
	defer a.writeLock.Unlock()

	for _, t := range a.tunnels {
		t.keepalive()
	}
}

func (a *Account) rateLimitReset() {
	if a.rateLimit < 1 {
		return
	}

	a.writeLock.Lock()
	defer a.writeLock.Unlock()

	for _, t := range a.tunnels {
		t.rateLimitReset(a.rateLimit)
	}
}
