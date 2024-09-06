package server

import (
	"fmt"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
)

type Ustub struct {
	tun         *Tunnel
	conn        *net.UDPConn
	srcAddr     *net.UDPAddr
	lastActvity time.Time
}

func newUstub(tun *Tunnel, udpConn *net.UDPConn, srcAddr *net.UDPAddr) *Ustub {
	ustub := &Ustub{tun: tun, conn: udpConn, srcAddr: srcAddr}
	go ustub.proxy()
	return ustub
}

func (u *Ustub) writeTo(dest *net.UDPAddr, data []byte) error {
	conn := u.conn
	if conn == nil {
		return fmt.Errorf("Write udp conn == nil")
	}

	log.Infof("writeMessage to %s", dest.String())

	u.lastActvity = time.Now()

	destAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s", dest.String()))
	if err != nil {
		return err
	}

	wrote := 0
	l := len(data)
	for {
		n, err := conn.WriteToUDP(data[wrote:], destAddr)
		if err != nil {
			return err
		}

		wrote = wrote + n
		if wrote == l {
			break
		}
	}

	return nil
}

func (u *Ustub) onServerData(data []byte, dest *net.UDPAddr) {
	log.Debugf("onServerData dest %s", dest.String())

	u.lastActvity = time.Now()

	if u.tun != nil {
		u.tun.onServerUDPData(data, u.srcAddr, dest)
	} else {
		log.Errorf("Ustub.onServerData u.tun == nil")
	}
}

func (u *Ustub) proxy() {
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

func (u *Ustub) close() {
	u.conn.Close()
}
