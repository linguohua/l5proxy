package server

import (
	"fmt"
	"net"

	log "github.com/sirupsen/logrus"
)

type TCPServ struct {
	reverse *ReverseServ
}

func newTCPServ(reverse *ReverseServ, addrssMaps map[string]AddressMap) (*TCPServ, error) {
	s := &TCPServ{reverse: reverse}

	for endpoint, addrssmap := range addrssMaps {
		for src, serv := range addrssmap {

			srcAddr, err := net.ResolveTCPAddr("tcp", src)
			if err != nil {
				return nil, err
			}

			listener, err := net.Listen("tcp", serv)
			if err != nil {
				return nil, err
			}

			log.Infof("tcp proxy on %s", serv)

			go s.startTCPServer(listener, endpoint, srcAddr)
		}
	}

	return s, nil
}

func (tcpServ *TCPServ) startTCPServer(listener net.Listener, endpoint string, src *net.TCPAddr) {
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}
		go tcpServ.handleConnection(conn, endpoint, src)
	}
}

func (tcpServ *TCPServ) handleConnection(conn net.Conn, endpoint string, src *net.TCPAddr) {
	tunnel := tcpServ.reverse.getTunnel(endpoint)
	if tunnel == nil {
		log.Errorf("can not get tunnel for endpoint %s", endpoint)
		return
	}

	tunnel.acceptTCPConn(conn, src)
}
