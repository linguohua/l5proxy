package server

import (
	"fmt"
	"net"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"net/http"
)

var (
	upgrader      = websocket.Upgrader{} // use default options
	accountMap    = make(map[string]*Account)
	dnsServerAddr *net.UDPAddr

	reverseServer *ReverseServ = nil
)

type AddressMap map[string]string

func wsHandler(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("upgrade:%s", err)
		return
	}
	defer c.Close()

	var uuid = r.URL.Query().Get("uuid")
	if uuid == "" {
		log.Error("need uuid!")
		return
	}

	var endpoint = r.URL.Query().Get("endpoint")
	if endpoint == "" {
		log.Error("need endpoint!")
		return
	}

	account, ok := accountMap[uuid]
	if !ok {
		log.Errorf("no account found for uuid:%s", uuid)
		return
	}

	account.acceptWebsocket(c, reverseServer, endpoint)
}

// indexHandler responds to requests with our greeting.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	fmt.Fprint(w, "Hello, Stupid!")
}

func keepalive() {
	tick := 0
	for {
		time.Sleep(time.Second * 1)
		tick++

		for _, a := range accountMap {
			a.rateLimitReset()
		}

		if tick == 30 {
			tick = 0
			for _, a := range accountMap {
				a.keepalive()
			}
		}
	}
}

func setupBuiltinAccount(cfg *L5proxyConfig) {
	for _, a := range cfg.Accounts {
		accountMap[a.UUID] = newAccount(a)
	}

	log.Infof("load account ok, number of account:%d", len(cfg.Accounts))
}

func setupAddressMap(cfg *L5proxyConfig) (map[string]AddressMap, map[string]AddressMap) {

	tcpAddressMap := make(map[string]AddressMap)
	for _, endpointAdressMap := range cfg.TcpPortMaps {
		addressmap := tcpAddressMap[endpointAdressMap.Endpoint]
		if addressmap == nil {
			addressmap = make(AddressMap)
		}

		for _, v := range endpointAdressMap.AddressMaps {
			addressmap[v.External] = v.Internal
		}

		tcpAddressMap[endpointAdressMap.Endpoint] = addressmap
	}

	udpAddressMap := make(map[string]AddressMap)
	for _, endpointAdressMap := range cfg.UdpPortMaps {
		addressmap := udpAddressMap[endpointAdressMap.Endpoint]
		if addressmap == nil {
			addressmap = make(AddressMap)
		}

		for _, v := range endpointAdressMap.AddressMaps {
			addressmap[v.External] = v.Internal
		}

		udpAddressMap[endpointAdressMap.Endpoint] = addressmap
	}

	return tcpAddressMap, udpAddressMap
}

func setupReverseServAddressMap(cfg *L5proxyConfig) {
	tcpAddressMaps, udpAddressMaps := setupAddressMap(cfg)

	reverseServ, err := NewReverseServ(tcpAddressMaps, udpAddressMaps)
	if err != nil {
		log.Panicln("new udp reverse server failed:", err)
	}

	reverseServer = reverseServ
}

// CreateHTTPServer start http server
func CreateHTTPServer(cfg *L5proxyConfig) {
	setupBuiltinAccount(cfg)
	setupReverseServAddressMap(cfg)

	var err error
	dnsServerAddr, err = net.ResolveUDPAddr("udp", "8.8.8.8:53")
	if err != nil {
		log.Fatal("resolve dns server address failed:", err)
	}

	go keepalive()
	http.HandleFunc("/", indexHandler)
	http.HandleFunc(cfg.Server.WebsocketPath, wsHandler)
	log.Infof("server listen at:%s, path:%s", cfg.Server.Address, cfg.Server.WebsocketPath)
	log.Fatal(http.ListenAndServe(cfg.Server.Address, nil))
}
