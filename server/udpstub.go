package server

import (
	"fmt"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
)

type UdpStub struct {
	tun         *Tunnel
	conn        *net.UDPConn
	srcAddr     *net.UDPAddr
	lastActvity time.Time
}

func newUdpStub(tun *Tunnel, udpConn *net.UDPConn, srcAddr *net.UDPAddr) *UdpStub {
	ustub := &UdpStub{tun: tun, conn: udpConn, srcAddr: srcAddr}
	go ustub.proxy()
	return ustub
}

func (u *UdpStub) writeTo(dest *net.UDPAddr, data []byte) error {
	conn := u.conn
	if conn == nil {
		return fmt.Errorf("write udp conn == nil")
	}

	log.Infof("writeMessage to %s", dest.String())

	u.lastActvity = time.Now()

	_, err := conn.WriteToUDP(data, dest)
	return err
}

func (u *UdpStub) onServerData(data []byte, dest *net.UDPAddr) {
	log.Debugf("onServerData dest %s", dest.String())

	u.lastActvity = time.Now()

	if u.tun != nil {
		u.tun.onServerUDPData(data, u.srcAddr, dest)
	} else {
		log.Errorf("UdpStub.onServerData u.tun == nil")
	}
}

func (u *UdpStub) proxy() {
	conn := u.conn
	defer conn.Close()
	// defer u.cache.remove(u)
	// TODO: remove ustub from cache when conn close

	buffer := make([]byte, 4096)

	for {
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}

		dest := &net.UDPAddr{Port: addr.Port}
		if addr.IP.To4() != nil {
			dest.IP = addr.IP.To4()
		} else {
			dest.IP = addr.IP.To16()
		}
		u.onServerData(buffer[:n], dest)
	}

}

func (u *UdpStub) close() {
	u.conn.Close()
}
