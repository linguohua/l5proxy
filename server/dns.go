package server

import (
	"fmt"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
)

var dnsSlots = make(chan struct{}, 64)

func doDNSQuery(tun *Tunnel, dnsPacket []byte) {
	// the packet format: [cmd 1byte] + [address type 1byte] + [ip address n-bytes] + [port 2bytes]
	if len(dnsPacket) < 2 {
		return
	}
	var skipLength int
	addressType := dnsPacket[1]
	switch addressType {
	case 0: // ipv4
		skipLength = 4
	case 1: // ipv6
		skipLength = 16
	default:
		log.Errorf("doDNSQuery, unsupport address type:%d", addressType)
		return
	}

	skipLength = skipLength + 1 + 1 + 2 // cmd, address type, ip, port

	if len(dnsPacket) < skipLength {
		return
	}
	query := dnsPacket[skipLength:]
	response, err := sharedDNSCache.query(query, dnsServerAddr.String(), func() ([]byte, error) {
		return exchangeDNS(query, dnsServerAddr)
	})
	if err != nil {
		log.Warnf("DNS query failed: %s", err)
		return
	}
	reply := make([]byte, skipLength+len(response))
	copy(reply, dnsPacket[:skipLength])
	reply[0] = cMDDNSRsp
	copy(reply[skipLength:], response)
	if err := tun.write(reply); err != nil {
		log.Warnf("DNS response tunnel write failed: %s", err)
	}
}

var sharedDNSCache = newDNSCache()

func exchangeDNS(query []byte, upstream *net.UDPAddr) ([]byte, error) {
	if len(query) < 12 {
		return nil, fmt.Errorf("short DNS query")
	}
	conn, err := net.DialUDP("udp", nil, upstream)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, err
	}
	if _, err = conn.Write(query); err != nil {
		return nil, err
	}
	// Larger than any UDP payload; the L5 return envelope is allocated separately.
	buf := make([]byte, 65535)
	for {
		n, e := conn.Read(buf)
		if e != nil {
			return nil, e
		}
		// Ignore mismatched datagrams within the same bounded deadline.
		if dnsValidate(query, buf[:n]) != nil {
			continue
		}
		return buf[:n], nil
	}
}
