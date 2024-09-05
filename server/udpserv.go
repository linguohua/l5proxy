package server

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"

	log "github.com/sirupsen/logrus"
)

// key=hash(src)
type UDPConnMap map[string]*net.UDPConn
type TunnelMap map[int]*Tunnel

type UDPServ struct {
	udpConns map[string]UDPConnMap
	reverse  *ReverseServ
}

func newUDPServ(reverse *ReverseServ, addrssMaps map[string]AddressMap) (*UDPServ, error) {
	s := &UDPServ{udpConns: make(map[string]UDPConnMap), reverse: reverse}

	for endpoint, addrssmap := range addrssMaps {
		for src, serv := range addrssmap {
			conn, err := newUDPConn(serv)
			if err != nil {
				return nil, err
			}

			srcAddr, err := net.ResolveUDPAddr("udp", src)
			if err != nil {
				return nil, err
			}

			udpConnMap := s.udpConns[endpoint]
			if udpConnMap == nil {
				udpConnMap = make(UDPConnMap)
			}

			key := key(srcAddr)
			udpConnMap[key] = conn
			s.udpConns[endpoint] = udpConnMap

			go s.udpProxy(conn, srcAddr, endpoint)
		}
	}

	return s, nil
}

func newUDPConn(addr string) (*net.UDPConn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("Error resolving UDP address: %s", err.Error())
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("Error creating UDP connection: %s", err.Error())
	}

	log.Infof("udp proxy on %s", conn.LocalAddr().String())
	return conn, nil
}

func (udpServ *UDPServ) getUDPConn(endpoint string, src *net.UDPAddr) *net.UDPConn {
	udpConnMap := udpServ.udpConns[endpoint]
	if udpConnMap == nil {
		return nil
	}

	key := key(src)
	return udpConnMap[key]
}

func (udpServ *UDPServ) onData(msg []byte, srcAddr *net.UDPAddr, destAddr *net.UDPAddr, endpoint string) {
	tunnel := udpServ.reverse.getTunnel(endpoint)
	if tunnel == nil {
		log.Errorf("can not get tunnel for endpoint %s", endpoint)
		return
	}

	tunnel.onServerUDPData(msg, srcAddr, destAddr)
}

func (udpServ *UDPServ) udpProxy(conn *net.UDPConn, srcAddr *net.UDPAddr, endpoint string) {
	defer conn.Close()
	buffer := make([]byte, 4096)

	for {
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}

		// use len(dest.IP) check ipv4 or ipv6
		dest := &net.UDPAddr{Port: addr.Port}
		if addr.IP.To4() != nil {
			dest.IP = addr.IP.To4()
		} else {
			dest.IP = addr.IP.To16()
		}
		udpServ.onData(buffer[:n], srcAddr, dest, endpoint)
	}

}

func key(src *net.UDPAddr) string {
	buf := make([]byte, 2+len(src.IP))
	binary.LittleEndian.PutUint16(buf[0:], uint16(src.Port))
	copy(buf[2:], src.IP)

	return hex.EncodeToString(buf)
}
