package server

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	cMDNone              = 0
	cMDReqData           = 1
	cMDReqCreated        = 2
	cMDReqClientClosed   = 3
	cMDReqClientFinished = 4
	cMDReqServerFinished = 5
	cMDReqServerClosed   = 6
	cMDDNSReq            = 7
	cMDDNSRsp            = 8
	cMDUDPReq            = 9
	cMDReqDataExt        = 10
)

type TunnelCreateCtx struct {
	account     string
	id          int
	conn        *websocket.Conn
	cap         int
	rateLimit   int
	endpoint    string
	reverseServ *ReverseServ

	withTimestamp bool

	relayURL string
}

// Tunnel tunnel
type Tunnel struct {
	id   int
	conn *websocket.Conn
	reqq *Reqq

	writeLock sync.Mutex
	waitping  int

	rateLimit int
	rateQuota int

	rateChanLock sync.Mutex
	rateChan     chan []byte
	rateWg       *sync.WaitGroup

	udpCache    *UdpCache
	endpoint    string
	reverseServ *ReverseServ
	// reverseServ *UDPReverseServers

	withTimestamp bool
}

func newTunnel(cc *TunnelCreateCtx) (*Tunnel, error) {
	t := &Tunnel{
		id:            cc.id,
		conn:          cc.conn,
		rateLimit:     cc.rateLimit,
		rateQuota:     cc.rateLimit,
		udpCache:      newUdpCache(),
		endpoint:      cc.endpoint,
		reverseServ:   cc.reverseServ,
		withTimestamp: cc.withTimestamp,
	}

	reqq := newReqq(cc.cap, t)
	t.reqq = reqq

	cc.conn.SetPingHandler(func(data string) error {
		t.writePong([]byte(data))
		return nil
	})

	cc.conn.SetPongHandler(func(data string) error {
		t.onPong([]byte(data))
		return nil
	})

	if cc.rateLimit > 0 {
		// 100 item
		t.rateChan = make(chan []byte, 100)
	}

	cc.reverseServ.onTunnelConnect(t)
	return t, nil
}

func (t *Tunnel) idx() int {
	return t.id
}

func (t *Tunnel) rateLimitReset(quota int) {
	t.rateQuota = quota

	if t.rateWg != nil {
		t.rateWg.Done()
		t.rateWg = nil
	}
}

func (t *Tunnel) waitQuota(q int) {
	if t.rateQuota < q {
		t.rateQuota = 0

		t.rateChanLock.Lock()
		if t.rateChan == nil {
			t.rateChanLock.Unlock()
			return
		}

		// wait
		wg := sync.WaitGroup{}
		wg.Add(1)
		t.rateWg = &wg

		t.rateChanLock.Unlock()

		wg.Wait()
	} else {
		t.rateQuota = t.rateQuota - q
	}
}

func (t *Tunnel) drainRateChan() {
	for {
		buf, ok := <-t.rateChan

		if !ok {
			// channel has been closed
			break
		}

		if buf != nil {
			leN := len(buf)
			err := t.write(buf)

			if err != nil {
				break
			}

			t.waitQuota(leN)
		} else {
			break
		}
	}
}

func (t *Tunnel) serve() {
	// loop read websocket message
	if t.rateLimit > 0 {
		go t.drainRateChan()
	}

	c := t.conn
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Errorf("Tunnel read failed:%s", err)
			break
		}

		// log.Infof("Tunnel recv message, len:%d", len(message))
		err = t.onTunnelMessage(message)
		if err != nil {
			log.Errorf("Tunnel onTunnelMessage failed:%s", err)
			break
		}
	}

	t.onClose()
}

func (t *Tunnel) keepalive() {
	if t.waitping > 3 {
		log.Errorf("Tunnel keepalive failed")
		t.conn.Close()
		return
	}

	t.writeLock.Lock()
	now := time.Now().Unix()
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(now))
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	t.conn.WriteMessage(websocket.PingMessage, b)
	t.writeLock.Unlock()

	t.waitping++

	t.udpCache.keepalive()
}

func (t *Tunnel) writePong(msg []byte) {
	t.writeLock.Lock()
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	t.conn.WriteMessage(websocket.PongMessage, msg)
	t.writeLock.Unlock()
}

func (t *Tunnel) write(msg []byte) error {
	t.writeLock.Lock()
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.conn.WriteMessage(websocket.BinaryMessage, msg)
	t.writeLock.Unlock()

	return err
}

func (t *Tunnel) onPong(_ []byte) {
	t.waitping = 0
}

func (t *Tunnel) onClose() {
	if t.rateLimit > 0 && t.rateChan != nil {
		t.rateChanLock.Lock()
		t.rateLimit = 0
		close(t.rateChan)
		t.rateChan = nil

		if t.rateWg != nil {
			t.rateWg.Done()
		}

		t.rateChanLock.Unlock()
	}

	t.reqq.cleanup()
	t.reverseServ.onTunnelClose(t)
}

func (t *Tunnel) onTunnelMessage(message []byte) error {
	if len(message) < 5 {
		return fmt.Errorf("invalid tunnel message")
	}

	t.waitping = 0

	cmd := message[0]
	if cmd == cMDDNSReq {
		// TODO: too many goroutines
		go doDNSQuery(t, message)
		return nil
	}

	if cmd == cMDUDPReq {
		t.handleUDPReq(message)
		return nil
	}

	idx := binary.LittleEndian.Uint16(message[1:])
	tag := binary.LittleEndian.Uint16(message[3:])

	switch cmd {
	case cMDReqCreated:
		t.handleClientReqCreate(idx, tag, message[5:])
	case cMDReqData:
		t.handleClientReqData(idx, tag, message[5:])
	case cMDReqDataExt:
		t.handleClientReqDataExt(idx, tag, message[5:])
	case cMDReqClientFinished:
		t.handleClientReqFinished(idx, tag)
	case cMDReqClientClosed:
		t.handleClientReqClosed(idx, tag)

	default:
		log.Infof("onTunnelMessage, unsupport tunnel cmd:%d", cmd)
	}

	return nil
}

func (t *Tunnel) handleClientReqCreate(idx uint16, tag uint16, message []byte) {
	addressType := message[0]
	var port uint16
	var domain string
	switch addressType {
	case 0: // ipv4
		domain = fmt.Sprintf("%d.%d.%d.%d", message[1], message[2], message[3], message[4])
		port = binary.LittleEndian.Uint16(message[5:])
	case 1: // domain name
		domainLen := message[1]
		domain = string(message[2 : 2+domainLen])
		port = binary.LittleEndian.Uint16(message[(2 + domainLen):])
	case 2: // ipv6
		p1 := binary.LittleEndian.Uint16(message[1:])
		p2 := binary.LittleEndian.Uint16(message[3:])
		p3 := binary.LittleEndian.Uint16(message[5:])
		p4 := binary.LittleEndian.Uint16(message[7:])
		p5 := binary.LittleEndian.Uint16(message[9:])
		p6 := binary.LittleEndian.Uint16(message[11:])
		p7 := binary.LittleEndian.Uint16(message[13:])
		p8 := binary.LittleEndian.Uint16(message[15:])

		domain = fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d", p8, p7, p6, p5, p4, p3, p2, p1)
		port = binary.LittleEndian.Uint16(message[17:])
	default:
		log.Errorf("handleRequestCreate, not support addressType:%d", addressType)
		return
	}

	req, err := t.reqq.alloc(idx, tag)
	if err != nil {
		log.Errorf("handleRequestCreate, alloc req failed:%v", err)
		return
	}

	addr := fmt.Sprintf("%s:%d", domain, port)

	now := time.Now()
	ts := time.Second * 2
	c, err := net.DialTimeout("tcp", addr, ts)
	if err != nil {
		log.Errorf("proxy DialTCP failed: %s", err)
		t.onServerReqTerminate(req)
		return
	}

	cxt := &ProxyContext{
		To:       addr,
		DialTime: time.Since(now),
	}

	req.conn = c.(*net.TCPConn)
	req.ctx = cxt

	go req.proxy()
}

func (t *Tunnel) handleClientReqData(idx uint16, tag uint16, message []byte) {
	req, err := t.reqq.get(idx, tag)
	if err != nil {
		log.Errorf("handleRequestData, get req failed:%s", err)
		return
	}

	req.onClientData(message)
}

func (t *Tunnel) handleClientReqDataExt(idx, tag uint16, msg []byte) {
	req, err := t.reqq.get(idx, tag)
	if err != nil {
		log.Debugf("handleClientReqDataExt error:%v", err)
		return
	}

	t.dumpTimestamp(req, msg)

	cut := len(msg) - (8 + 4*2)
	req.onClientData(msg[0:cut])
}

func (t *Tunnel) dumpTimestamp(req *Request, msg []byte) {
	ctx := req.ctx
	if ctx == nil {
		return
	}

	extraBytesLen := 8 + 4*2
	size := len(msg)

	if size <= extraBytesLen {
		log.Errorf("tunnel %d dumpTimestamp failed, size %d not enough", t.id, size)
		return
	}

	offset := size - extraBytesLen
	unixMilli := binary.LittleEndian.Uint64(msg[offset:])
	offset = offset + 8

	unixMilliNow := time.Now().UnixMilli()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tunnel[%d] [%s]-->[%s],size:%d, timestamps[ms]:", t.id,
		t.endpoint, ctx.To, size-extraBytesLen))

	for i := 0; i < 4; i++ {
		ts := binary.LittleEndian.Uint16(msg[offset:])
		if ts == 0 {
			break
		}

		offset = offset + 2
		sb.WriteString(fmt.Sprintf("%d,", ts))
	}

	sb.WriteString(fmt.Sprintf("%d", unixMilliNow-int64(unixMilli)))

	log.Infof(sb.String())
}

func (t *Tunnel) handleClientReqFinished(idx uint16, tag uint16) {
	req, err := t.reqq.get(idx, tag)
	if err != nil {
		//log.Errorf("handleRequestData, get req failed:%s", err)
		return
	}

	req.onClientFinished()
}

func (t *Tunnel) handleClientReqClosed(idx uint16, tag uint16) {
	err := t.reqq.free(idx, tag)
	if err != nil {
		//log.Errorf("handleRequestClosed, get req failed:%s", err)
		return
	}
}

func (t *Tunnel) onServerReqTerminate(req *Request) {
	// send close to client
	buf := make([]byte, 5)
	buf[0] = cMDReqServerClosed
	binary.LittleEndian.PutUint16(buf[1:], req.idx)
	binary.LittleEndian.PutUint16(buf[3:], req.tag)

	t.write(buf)

	t.handleClientReqClosed(req.idx, req.tag)
}

func (t *Tunnel) onRequestHalfClosed(req *Request) {
	// send half-close to client
	buf := make([]byte, 5)
	buf[0] = cMDReqServerFinished
	binary.LittleEndian.PutUint16(buf[1:], req.idx)
	binary.LittleEndian.PutUint16(buf[3:], req.tag)

	t.write(buf)
}

func (t *Tunnel) safeWriteRateChan(data []byte) error {
	t.rateChanLock.Lock() // lock to prevent write to a closed channel
	ch := t.rateChan
	t.rateChanLock.Unlock()

	var err error
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("tunnel has closed")
		}
	}()

	if ch != nil {
		ch <- data
	} else {
		err = fmt.Errorf("tunnel has closed")
	}

	return err
}

func (t *Tunnel) onServerReqData(req *Request, data []byte) error {
	extraBytesLen := 0
	if t.withTimestamp {
		extraBytesLen = 8 + 4*2
	}

	cmdAndDataLen := 5 + len(data)

	buf := make([]byte, cmdAndDataLen+extraBytesLen)

	if t.withTimestamp {
		buf[0] = cMDReqDataExt
	} else {
		buf[0] = cMDReqData
	}

	binary.LittleEndian.PutUint16(buf[1:], req.idx)
	binary.LittleEndian.PutUint16(buf[3:], req.tag)
	copy(buf[5:], data)
	if t.withTimestamp {
		// write timestamp
		timestamp := uint64(time.Now().UnixMilli())
		binary.LittleEndian.PutUint64(buf[cmdAndDataLen:], timestamp)
	}

	if t.rateLimit > 0 {
		return t.safeWriteRateChan(buf)
	} else {
		return t.write(buf)
	}
}

func (t *Tunnel) handleUDPReq(data []byte) {
	// skip cmd
	src := parseAddress(data[1:])

	srcIPLen := net.IPv6len
	if src.IP.To4() != nil {
		srcIPLen = net.IPv4len
	}

	// 4 = cmd + iptype + port
	dst := parseAddress(data[4+srcIPLen:])

	destIPLen := net.IPv6len
	if dst.IP.To4() != nil {
		destIPLen = net.IPv4len
	}

	log.Debugf("handleUDPReq src %s dst %s", src.String(), dst.String())

	// 7 = cmd + iptype1 + iptype2 + port1 + port2
	skip := 7 + srcIPLen + destIPLen

	// write to reverse proxy of udp conn
	conn := t.reverseServ.getUDPConn(t.endpoint, src)
	if conn != nil {
		conn.WriteTo(data[skip:], dst)
		return
	}

	ustub := t.udpCache.get(src)
	if ustub == nil {
		udpConn, err := newUDPConn("0.0.0.0:0")
		if err != nil {
			log.Errorf("New UDPConn failed:%s", err.Error())
			return
		}
		ustub = newUdpStub(t, udpConn, src)
		t.udpCache.add(ustub)
		log.Infof("new ustub src %s", src.String())
	}

	ustub.writeTo(dst, data[skip:])
}

func (t *Tunnel) onServerUDPData(data []byte, src *net.UDPAddr, dst *net.UDPAddr) {
	log.Debugf("onServerData src %s dst %s", src, dst)

	srcAddrBuf := writeUDPAddress(src)
	destAddrBuf := writeUDPAddress(dst)

	buf := make([]byte, 1+len(srcAddrBuf)+len(destAddrBuf)+len(data))

	buf[0] = byte(cMDUDPReq)
	copy(buf[1:], srcAddrBuf)
	copy(buf[1+len(srcAddrBuf):], destAddrBuf)
	copy(buf[1+len(srcAddrBuf)+len(destAddrBuf):], data)

	_ = t.write(buf)
}

func parseAddress(msg []byte) *net.UDPAddr {
	// skip cmd
	var ip []byte = nil

	offset := 0
	port := binary.LittleEndian.Uint16(msg[offset:])
	offset += 2

	ipType := msg[offset]
	offset += 1

	switch ipType {
	case 0:
		// ipv4
		ip = make([]byte, net.IPv4len)
		copy(ip[0:], msg[offset:offset+4])
	case 2:
		// ipv6
		ip = make([]byte, net.IPv6len)
		copy(ip[0:], msg[offset:offset+16])
	}

	return &net.UDPAddr{IP: ip, Port: int(port)}
}

func writeUDPAddress(address *net.UDPAddr) []byte {
	iplen := net.IPv6len
	if address.IP.To4() != nil {
		iplen = net.IPv4len
	}
	// 3 = iptype(1) + port(2)
	buf := make([]byte, 3+iplen)
	// add port
	binary.LittleEndian.PutUint16(buf[0:], uint16(address.Port))
	// set ip type
	if iplen > net.IPv4len {
		// ipv6
		buf[2] = 2
		copy(buf[3:], address.IP.To16())
	} else {
		// ipv4
		buf[2] = 0
		copy(buf[3:], address.IP.To4())
	}

	return buf
}

func writeTCPAddress(address *net.TCPAddr) []byte {
	iplen := net.IPv6len
	if address.IP.To4() != nil {
		iplen = net.IPv4len
	}
	// 3 = iptype(1) + port(2)
	buf := make([]byte, 3+iplen)
	// add port
	binary.LittleEndian.PutUint16(buf[0:], uint16(address.Port))
	// set ip type
	if iplen > net.IPv4len {
		// ipv6
		buf[2] = 2
		copy(buf[3:], address.IP.To16())
	} else {
		// ipv4
		buf[2] = 0
		copy(buf[3:], address.IP.To4())
	}

	return buf
}

func (t *Tunnel) acceptTCPConn(conn net.Conn, src *net.TCPAddr) error {
	tcpConn := conn.(*net.TCPConn)
	req, err := t.reqq.allocForConn(tcpConn)
	if err != nil {
		return err
	}

	addr := conn.RemoteAddr()

	dst, ok := addr.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("can not convert net.Addr to net.TCPAddr")
	}

	t.onClientCreate(src, dst, req.idx, req.tag)

	// start a new goroutine to read data from 'conn'
	ctx := &ProxyContext{
		To: dst.String(),
	}
	req.ctx = ctx
	go req.proxy()

	return nil
}

func (t *Tunnel) onClientCreate(src, dst *net.TCPAddr, idx, tag uint16) {
	// log.Infof("Tunnel.onClientCreate src", src.String())

	srcAddrBuf := writeTCPAddress(src)
	destAddrBuf := writeTCPAddress(dst)

	buf := make([]byte, 5+len(srcAddrBuf)+len(destAddrBuf))

	buf[0] = byte(cMDReqCreated)
	binary.LittleEndian.PutUint16(buf[1:], idx)
	binary.LittleEndian.PutUint16(buf[3:], tag)

	copy(buf[5:], srcAddrBuf)
	copy(buf[5+len(srcAddrBuf):], destAddrBuf)

	_ = t.write(buf)
}
