package server

import (
	"fmt"
	"net"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

type Ustub struct {
	tun         *Tunnel
	conn        *net.UDPConn
	srcAddr     *net.UDPAddr
	lastActvity time.Time
}

func newUstub(tun *Tunnel, srcAddr *net.UDPAddr) *Ustub {
	udpAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		fmt.Println("Error resolving UDP address:", err)
		os.Exit(1)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		fmt.Println("Error creating UDP connection:", err)
		os.Exit(1)
	}

	fmt.Printf("udp proxy on %s\n", conn.LocalAddr().String())

	ustub := &Ustub{tun: tun, conn: conn, srcAddr: srcAddr}
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
	log.Println("onServerData dest ", dest.String())

	u.lastActvity = time.Now()
	u.tun.onServerData(data, u.srcAddr, dest)
}

func (u *Ustub) proxy() {
	conn := u.conn

	defer conn.Close()

	buffer := make([]byte, 4096)

	for {
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}

		u.onServerData(buffer[:n], addr)
	}
}

func (u *Ustub) close() {
	u.conn.Close()
}
