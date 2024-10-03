package server

import (
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

type ITunnel interface {
	serve()
	keepalive()
	rateLimitReset(int)
	idx() int
}

// Account account
type Account struct {
	uuid    string
	tunnels map[int]ITunnel
	tidx    int

	rateLimit int // 0: no limit
	maxTunnel int

	relayURL string

	tunnelInBuilding int
	writeLock        sync.Mutex
}

func newAccount(uc *AccountTomlConfig) *Account {
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
	tunnelCount := len(a.tunnels) + a.tunnelInBuilding
	idx := a.tidx
	a.tidx++
	a.writeLock.Unlock()

	defer func() {
		a.writeLock.Lock()
		a.tunnelInBuilding--
		a.writeLock.Unlock()
	}()

	if a.maxTunnel > 0 && tunnelCount > a.maxTunnel {
		conn.Close()
		return nil, fmt.Errorf("too many tunnels")
	}

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
	tunnels := make([]ITunnel, 0, len(a.tunnels))
	for _, v := range a.tunnels {
		tunnels = append(tunnels, v)
	}
	a.writeLock.Unlock()

	for _, t := range tunnels {
		t.keepalive()
	}
}

func (a *Account) rateLimitReset() {
	if a.rateLimit < 1 {
		return
	}

	a.writeLock.Lock()
	tunnels := make([]ITunnel, 0, len(a.tunnels))
	for _, v := range a.tunnels {
		tunnels = append(tunnels, v)
	}
	a.writeLock.Unlock()

	for _, t := range tunnels {
		t.rateLimitReset(a.rateLimit)
	}
}
