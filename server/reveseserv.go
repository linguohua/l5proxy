package server

import (
	"net"
	"sync"
)

type ReverseServ struct {
	udpServ *UDPServ
	tcpServ *TCPServ

	tunnels map[string]TunnelMap
	lock    sync.Mutex
}

func NewReverseServ(tcpAddrssMaps, udpAddrssMaps map[string]AddressMap) (*ReverseServ, error) {
	rServ := &ReverseServ{tunnels: make(map[string]TunnelMap)}

	tcpServer, err := newTCPServ(rServ, tcpAddrssMaps)
	if err != nil {
		return nil, err
	}
	rServ.tcpServ = tcpServer

	udpServer, err := newUDPServ(rServ, udpAddrssMaps)
	if err != nil {
		return nil, err
	}
	rServ.udpServ = udpServer

	return rServ, nil
}

func (serv *ReverseServ) onTunnelConnect(tunnel *Tunnel) {
	serv.lock.Lock()
	defer serv.lock.Unlock()

	endpoint := tunnel.endpoint

	tunnelMap := serv.tunnels[endpoint]
	if tunnelMap == nil {
		tunnelMap = make(TunnelMap)
	}

	tunnelMap[tunnel.id] = tunnel
	serv.tunnels[endpoint] = tunnelMap
}

func (serv *ReverseServ) onTunnelClose(tunnel *Tunnel) {
	serv.lock.Lock()
	defer serv.lock.Unlock()

	endpoint := tunnel.endpoint

	tunnelMap := serv.tunnels[endpoint]
	if tunnelMap == nil {
		return
	}

	delete(tunnelMap, tunnel.id)

	if len(tunnelMap) > 0 {
		serv.tunnels[endpoint] = tunnelMap
	} else {
		delete(serv.tunnels, endpoint)
	}
}

func (serv *ReverseServ) getTunnel(endpoint string) *Tunnel {
	tunnelMap := serv.tunnels[endpoint]
	if tunnelMap == nil {
		return nil
	}

	for _, v := range tunnelMap {
		return v
	}

	return nil
}

func (serv *ReverseServ) getUDPConn(endpoint string, src *net.UDPAddr) *net.UDPConn {
	return serv.udpServ.getUDPConn(endpoint, src)
}
