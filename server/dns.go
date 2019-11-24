package server

import (
	"net"
	"time"

	log "github.com/sirupsen/logrus"
)

func doDNSQuery(tun *Tunnel, dnsPacket []byte) {
	// the packet format: [cmd 1byte] + [address type 1byte] + [ip address n-bytes] + [port 2bytes]
	var skipLength int
	addressType := dnsPacket[1]
	switch addressType {
	case 0: // ipv4
		skipLength = 4
	case 1: // ipv6
		skipLength = 16
	default:
		log.Println("doDNSQuery, unsupport address type:", addressType)
		return
	}

	skipLength = skipLength + 1 + 1 + 2 // cmd, address type, ip, port

	conn, err := net.DialUDP("udp", nil, dnsServerAddr)
	if err != nil {
		log.Println("doDNSQuery, DialUDP failed:", err)
		return
	}

	// log.Println("doDNSQuery, dns query lenght:", len(dnsPacket)-skipLength)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(dnsPacket[skipLength:])
	if err != nil {
		log.Println("doDNSQuery, Write failed:", err)
		return
	}

	b := make([]byte, 600) // 600 is enough for DNS query reply
	n, err := conn.Read(b[skipLength:])
	if err != nil {
		log.Println("doDNSQuery, Read failed:", err)
		return
	}

	// reply to client
	copy(b[0:skipLength], dnsPacket[0:skipLength])
	b[0] = cMDDNSRsp
	n = n + skipLength

	tun.write(b[0:n])
}
