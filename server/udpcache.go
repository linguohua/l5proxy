package server

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"sync"
	"time"
)

const (
	udpTimeOut            = 120 * time.Second
	udpCacheKeepaliveTime = 30 * time.Second
)

type UdpCache struct {
	// key=hash(src+dest)
	ustubs sync.Map

	lastKeepalive time.Time
}

func newUdpCache() *UdpCache {
	return &UdpCache{
		lastKeepalive: time.Now(),
	}
}

func (c *UdpCache) add(ustub *UdpStub) {
	key := c.key(ustub.srcAddr)
	// log.Infof("add key %s", key)
	c.ustubs.Store(key, ustub)
}

func (c *UdpCache) get(src *net.UDPAddr) *UdpStub {
	key := c.key(src)
	v, ok := c.ustubs.Load(key)
	if ok {
		return v.(*UdpStub)
	}
	return nil
}

// func (c *UdpCache) remove(ustub *UdpStub) {
// 	key := c.key(ustub.srcAddr)
// 	c.ustubs.Delete(key)
// }

func (c *UdpCache) keepalive() {
	if time.Since(c.lastKeepalive) < udpCacheKeepaliveTime {
		return
	}

	deleteKeys := make([]string, 0, 128)
	c.ustubs.Range(func(key, value interface{}) bool {
		ustub := value.(*UdpStub)
		if time.Since(ustub.lastActvity) > udpTimeOut {
			deleteKeys = append(deleteKeys, key.(string))
		}

		return true // Continue iterating
	})

	for _, key := range deleteKeys {
		v, ok := c.ustubs.Load(key)
		if ok {
			ustub := v.(*UdpStub)
			ustub.close()
		}
		c.ustubs.Delete(key)
	}

	c.lastKeepalive = time.Now()
}

func (c *UdpCache) key(src *net.UDPAddr) string {
	iplen := net.IPv6len
	if src.IP.To4() != nil {
		iplen = net.IPv4len
	}

	buf := make([]byte, 2+iplen)
	binary.LittleEndian.PutUint16(buf[0:], uint16(src.Port))

	if iplen == net.IPv4len {
		copy(buf[2:], src.IP.To4())
	} else {
		copy(buf[2:], src.IP.To16())
	}

	return hex.EncodeToString(buf)
}

// func (c *UdpCache) cleanup() {
// 	c.ustubs.Range(func(key, value interface{}) bool {
// 		ustub := value.(*UdpStub)
// 		ustub.close()
// 		return true // Continue iterating
// 	})
// }
