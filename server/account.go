package server

import (
	"fmt"
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
	idx() int
}

// Account account
type Account struct {
	uuid    string
	tunnels map[int]ITunnel
	tidx    int

	rateLimit uint // 0: no limit
	maxTunnel uint

	relayURL string

	tunnelInBuilding int
	writeLock        sync.Mutex
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

func (a *Account) buildTunnel(conn *websocket.Conn, reverseServ *ReverseServ, endpoint string) (ITunnel, error) {
	a.writeLock.Lock()
	a.tunnelInBuilding++
	defer func() {
		a.tunnelInBuilding--
		a.writeLock.Unlock()
	}()

	if a.maxTunnel > 0 && uint(len(a.tunnels)+a.tunnelInBuilding) > a.maxTunnel {
		conn.Close()
		return nil, fmt.Errorf("too many tunnels")
	}

	idx := a.tidx
	a.tidx++

	var tun ITunnel
	var err error
	if len(a.relayURL) > 0 {
		// in relay-model
		tun, err = newRelayTunnel(idx, conn, endpoint, a.uuid, a.relayURL)
	} else {
		tun, err = newTunnel(idx, conn, 200, a.rateLimit, endpoint, reverseServ)
	}

	return tun, err
}

func (a *Account) acceptWebsocket(conn *websocket.Conn, reverseServ *ReverseServ, endpoint string) {
	log.Infof("account:%s try to accept websocket, endpoint:%s", a.uuid, endpoint)

	tun, err := a.buildTunnel(conn, reverseServ, endpoint)
	if err != nil {
		log.Errorf("account %s accept websocket failed cause create tunnel failed:%v", a.uuid, err)
		return
	}

	idx := tun.idx()

	a.writeLock.Lock()
	a.tunnels[idx] = tun
	log.Infof("account:%s accept websocket, total:%d", a.uuid, len(a.tunnels))
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
