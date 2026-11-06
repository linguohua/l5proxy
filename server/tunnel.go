package server

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
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
)

const heartbeatHeader = "X-L5-Heartbeat"
const heartbeatCapability = "client-idle-v1" // compatibility with the previous client
const screenHeartbeatCapability = "screen-idle-v2"
const serverPingHeader = "X-L5-Server-Ping-Seconds"
const screenHeader = "X-L5-Screen-Interactive"

type heartbeatOptions struct {
	diagnosticID string
	clientIdle   bool
	interval     time.Duration
	asleep       bool
}

// Client idle <= 300s, alignment <= 5s, probe response <= 30s, plus scheduling margin.
const clientHeartbeatReadTimeout = 390 * time.Second

type TunnelCreateCtx struct {
	heartbeat   heartbeatOptions
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
	diagnostic *tunnelDiagnostic
	id         int
	conn       *websocket.Conn
	reqq       *Reqq

	writeLock       sync.Mutex
	waitping        atomic.Int32
	clientHeartbeat bool
	heartbeat       heartbeatOptions
	heartbeatLock   sync.Mutex
	lastRx          atomic.Int64
	lastTx          atomic.Int64
	probeAt         time.Time

	rateLimit int
	rateQuota int

	rateChanLock sync.Mutex
	rateChan     chan []byte
	rateWg       *sync.WaitGroup
	rateDone     chan struct{}
	rateClose    sync.Once

	udpCache    *UdpCache
	endpoint    string
	reverseServ *ReverseServ
	// reverseServ *UDPReverseServers

	withTimestamp bool
}

func newTunnel(cc *TunnelCreateCtx) (*Tunnel, error) {
	t := &Tunnel{
		clientHeartbeat: cc.heartbeat.clientIdle,
		heartbeat:       cc.heartbeat,
		id:              cc.id,
		conn:            cc.conn,
		rateLimit:       cc.rateLimit,
		rateQuota:       cc.rateLimit,
		udpCache:        newUdpCache(),
		endpoint:        cc.endpoint,
		reverseServ:     cc.reverseServ,
		withTimestamp:   cc.withTimestamp,
	}

	t.diagnostic = newTunnelDiagnostic(cc.heartbeat.diagnosticID, cc.heartbeat.asleep, cc.conn.Close)
	t.lastRx.Store(time.Now().UnixNano())
	t.lastTx.Store(time.Now().UnixNano())
	reqq := newReqq(cc.cap, t)
	t.reqq = reqq

	cc.conn.SetPingHandler(func(data string) error {
		t.diagnostic.event("ping-rx")
		t.refreshHeartbeatDeadline()
		// Screen notices are diagnostic metadata only. The negotiated server
		// heartbeat interval applies regardless of the client's screen state.
		if t.heartbeat.interval > 0 {
			if data == "l5:sleep" {
				t.diagnostic.event("sleep")
			}
			if data == "l5:awake" {
				t.diagnostic.event("awake")
			}
		}

		return t.writePong([]byte(data))
	})

	cc.conn.SetPongHandler(func(data string) error {
		t.diagnostic.event("pong-rx")
		t.refreshHeartbeatDeadline()
		t.onPong([]byte(data))
		return nil
	})

	if cc.rateLimit > 0 {
		// 100 item
		t.rateChan = make(chan []byte, 100)
		t.rateDone = make(chan struct{})
	}

	cc.reverseServ.onTunnelConnect(t)
	return t, nil
}

func (t *Tunnel) idx() int {
	return t.id
}

func (t *Tunnel) rateLimitReset(quota int) {
	t.rateChanLock.Lock()
	defer t.rateChanLock.Unlock()
	t.rateQuota = quota

	if t.rateWg != nil {
		t.rateWg.Done()
		t.rateWg = nil
	}
}

func (t *Tunnel) waitQuota(q int) {
	t.rateChanLock.Lock()
	if t.rateQuota < q {
		t.rateQuota = 0

		if t.rateChan == nil {
			t.rateChanLock.Unlock()
			return
		}

		// wait
		wg := sync.WaitGroup{}
		wg.Add(1)
		t.rateWg = &wg

		t.rateChanLock.Unlock()

		select {
		case <-t.rateDone:
			return
		default:
		}
		wg.Wait()
	} else {
		t.rateQuota = t.rateQuota - q
		t.rateChanLock.Unlock()
	}
}

func (t *Tunnel) drainRateChan() {
	for {
		var buf []byte
		var ok bool
		select {
		case <-t.rateDone:
			return
		case buf, ok = <-t.rateChan:
		}

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
	t.refreshHeartbeatDeadline()
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Errorf("Tunnel read failed:%s", err)
			break
		}

		t.refreshHeartbeatDeadline()
		// log.Infof("Tunnel recv message, len:%d", len(message))
		err = t.onTunnelMessage(message)
		if err != nil {
			log.Errorf("Tunnel onTunnelMessage failed:%s", err)
			break
		}
	}

	t.onClose()
}

// Called only from the connection reader and its control handlers (Gorilla read ownership).
func (t *Tunnel) refreshHeartbeatDeadline() {
	t.diagnostic.event("rx")
	t.lastRx.Store(time.Now().UnixNano())
	if t.clientHeartbeat {
		t.conn.SetReadDeadline(time.Now().Add(clientHeartbeatReadTimeout))
	}
}

func (t *Tunnel) keepalive() {
	t.udpCache.keepalive()
	if t.heartbeat.interval > 0 {
		t.intervalKeepalive(time.Now())
		return
	}
	if t.clientHeartbeat {
		return
	}
	if t.waitping.Load() > 3 {
		log.Errorf("Tunnel keepalive failed")
		t.conn.Close()
		return
	}

	t.waitping.Add(1)
	t.writeLock.Lock()
	now := time.Now().UnixMilli()
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(now))
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	if t.conn.WriteMessage(websocket.PingMessage, b) == nil {
		t.diagnostic.event("ping-tx")
		t.diagnostic.event("tx")
	}
	t.writeLock.Unlock()
}

// The negotiated interval applies even while the client is asleep.
// Probe response deadlines reclaim stale connections without a fixed read timeout.
// Called by the existing server maintenance tick; never schedules a phone-side timer.
func (t *Tunnel) intervalKeepalive(now time.Time) {
	t.heartbeatLock.Lock()
	defer t.heartbeatLock.Unlock()
	rx := time.Unix(0, t.lastRx.Load())
	tx := time.Unix(0, t.lastTx.Load())
	if !t.probeAt.IsZero() {
		if rx.After(t.probeAt) {
			t.probeAt = time.Time{}
		} else {
			if now.Sub(t.probeAt) >= 30*time.Second {
				t.conn.Close()
			}
			return
		}
	}
	if now.Sub(tx) < t.heartbeat.interval {
		return
	}
	// Mark before sending so a fast Pong cannot be lost.
	t.probeAt = now
	if err := t.conn.WriteControl(websocket.PingMessage, []byte("l5:probe"), time.Now().Add(websocketWriteDealine*time.Second)); err != nil {
		t.conn.Close()
		return
	}
	t.diagnostic.event("ping-tx")
	t.diagnostic.event("tx")
	t.lastTx.Store(time.Now().UnixNano())
}

func (t *Tunnel) writePong(msg []byte) error {
	err := t.conn.WriteControl(websocket.PongMessage, msg, time.Now().Add(websocketWriteDealine*time.Second))
	if err == nil {
		t.diagnostic.event("pong-tx")
		t.diagnostic.event("tx")
		t.lastTx.Store(time.Now().UnixNano())
	}
	return err
}

func (t *Tunnel) write(msg []byte) error {
	t.writeLock.Lock()
	t.conn.SetWriteDeadline(time.Now().Add(websocketWriteDealine * time.Second))
	err := t.conn.WriteMessage(websocket.BinaryMessage, msg)
	if err == nil {
		t.diagnostic.event("tx")
		t.lastTx.Store(time.Now().UnixNano())
	}
	t.writeLock.Unlock()

	return err
}

func (t *Tunnel) onPong(_ []byte) {
	t.waitping.Store(0)
}

func (t *Tunnel) onClose() {
	t.diagnostic.finish()
	if t.rateDone != nil {
		t.rateClose.Do(func() { close(t.rateDone) })
		t.rateLimitReset(0)
	}

	t.reqq.cleanup()
	t.reverseServ.onTunnelClose(t)
}

func (t *Tunnel) onTunnelMessage(message []byte) error {
	if len(message) < 5 {
		return fmt.Errorf("invalid tunnel message")
	}

	t.waitping.Store(0)

	cmd := message[0]
	if cmd == cMDDNSReq {
		// TODO: too many goroutines
		select {
		case dnsSlots <- struct{}{}:
			go func() { defer func() { <-dnsSlots }(); doDNSQuery(t, message) }()
		default:
		}
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
	if len(message) < 1 {
		return
	}
	addressType := message[0]
	var port uint16
	var domain string
	switch addressType {
	case 0: // ipv4
		if len(message) < 7 {
			return
		}
		domain = fmt.Sprintf("%d.%d.%d.%d", message[1], message[2], message[3], message[4])
		port = binary.LittleEndian.Uint16(message[5:])
	case 1: // domain name
		if len(message) < 2 {
			return
		}
		domainLen := int(message[1])
		if len(message) < 4+domainLen {
			return
		}
		domain = string(message[2 : 2+domainLen])
		port = binary.LittleEndian.Uint16(message[(2 + domainLen):])
	case 2: // ipv6
		if len(message) < 19 {
			return
		}
		domain = net.IP(message[1:17]).String()
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

	addr := net.JoinHostPort(domain, strconv.Itoa(int(port)))

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
	select {
	case <-t.rateDone:
		return fmt.Errorf("tunnel has closed")
	case t.rateChan <- data:
		return nil
	}

}

func (t *Tunnel) onServerReqData(req *Request, data []byte) error {
	cmdAndDataLen := 5 + len(data)

	buf := make([]byte, cmdAndDataLen)

	buf[0] = cMDReqData
	binary.LittleEndian.PutUint16(buf[1:], req.idx)
	binary.LittleEndian.PutUint16(buf[3:], req.tag)
	copy(buf[5:], data)

	if t.rateLimit > 0 {
		return t.safeWriteRateChan(buf)
	} else {
		return t.write(buf)
	}
}

func (t *Tunnel) handleUDPReq(data []byte) {
	// skip cmd
	if len(data) < 1 {
		return
	}
	src := parseAddress(data[1:])
	if src == nil {
		return
	}

	srcIPLen := net.IPv6len
	if data[3] == 0 {
		srcIPLen = net.IPv4len
	}

	// 4 = cmd + iptype + port
	dst := parseAddress(data[4+srcIPLen:])
	if dst == nil {
		return
	}

	destIPLen := net.IPv6len
	if data[6+srcIPLen] == 0 {
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
	if len(msg) < 3 {
		return nil
	}
	size := 0
	switch msg[2] {
	case 0:
		size = 4
	case 2:
		size = 16
	default:
		return nil
	}
	if len(msg) < 3+size {
		return nil
	}
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
