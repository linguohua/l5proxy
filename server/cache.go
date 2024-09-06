package server

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"sync"
	"time"
)

const udpTimeOut = 120 * time.Second

type Cache struct {
	// key=hash(src+dest)
	ustubs sync.Map
}

func newCache() *Cache {
	return &Cache{}
}

func (c *Cache) add(ustub *Ustub) {
	key := c.key(ustub.srcAddr)
	// log.Infof("add key %s", key)
	c.ustubs.Store(key, ustub)
}

func (c *Cache) get(src *net.UDPAddr) *Ustub {
	key := c.key(src)
	v, ok := c.ustubs.Load(key)
	if ok {
		return v.(*Ustub)
	}
	return nil
}

func (c *Cache) remove(ustub *Ustub) {
	key := c.key(ustub.srcAddr)
	c.ustubs.Delete(key)
}

func (c *Cache) keepalive() {
	time.Sleep(time.Second * 30)

	deleteKeys := make([]string, 0)
	c.ustubs.Range(func(key, value interface{}) bool {
		ustub := value.(*Ustub)
		if time.Since(ustub.lastActvity) > udpTimeOut {
			deleteKeys = append(deleteKeys, key.(string))
		}

		return true // Continue iterating
	})

	for _, key := range deleteKeys {
		v, ok := c.ustubs.Load(key)
		if ok {
			ustub := v.(*Ustub)
			ustub.close()
		}
		c.ustubs.Delete(key)
	}
}

func (c *Cache) key(src *net.UDPAddr) string {
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

func (c *Cache) cleanup() {
	c.ustubs.Range(func(key, value interface{}) bool {
		ustub := value.(*Ustub)
		ustub.close()
		return true // Continue iterating
	})
}
