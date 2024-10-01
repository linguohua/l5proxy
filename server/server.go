package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path"
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

func setupBuiltinAccount(accountFilePath string) {
	fileBytes, err := os.ReadFile(accountFilePath)
	if err != nil {
		log.Panicln("read account cfg file failed:", err)
	}

	// uuids := []*AccountConfig{
	// 	{uuid: "ee80e87b-fc41-4e59-a722-7c3fee039cb4", rateLimit: 200 * 1024, maxTunnelCount: 3},
	// 	{uuid: "f6000866-1b89-4ab4-b1ce-6b7625b8259a", rateLimit: 0, maxTunnelCount: 3}}
	type jsonstruct struct {
		Accounts []*AccountConfig `json:"accounts"`
	}

	var accountCfgs = &jsonstruct{}
	err = json.Unmarshal(fileBytes, accountCfgs)
	if err != nil {
		log.Panicln("parse account cfg file failed:", err)
	}

	for _, a := range accountCfgs.Accounts {
		accountMap[a.UUID] = newAccount(a)
	}

	log.Infof("load account ok, number of account:%d", len(accountCfgs.Accounts))
}

func setupAddressMap(addressMapFilePath string) (map[string]AddressMap, map[string]AddressMap) {
	fileBytes, err := os.ReadFile(addressMapFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		log.Panicf("read addressmap cfg file %s failed:%s", addressMapFilePath, err.Error())
	}

	type EndpointAdressMap struct {
		Endpoint   string     `json:"endpoint"`
		AddressMap AddressMap `json:"addressmap"`
	}

	type jsonstruct struct {
		UDPAddressMaps []EndpointAdressMap `json:"udpmap"`
		TCPAddressMaps []EndpointAdressMap `json:"tcpmap"`
	}
	var endpointAddressMaps = jsonstruct{}
	err = json.Unmarshal(fileBytes, &endpointAddressMaps)
	if err != nil {
		log.Panicln("parse addressmap cfg file failed:", err)
	}

	tcpAddressMap := make(map[string]AddressMap)
	for _, endpointAdressMap := range endpointAddressMaps.TCPAddressMaps {
		addressmap := tcpAddressMap[endpointAdressMap.Endpoint]
		if addressmap == nil {
			tcpAddressMap[endpointAdressMap.Endpoint] = endpointAdressMap.AddressMap
			continue
		}

		for k, v := range endpointAdressMap.AddressMap {
			addressmap[k] = v
		}

		tcpAddressMap[endpointAdressMap.Endpoint] = addressmap
	}

	udpAddressMap := make(map[string]AddressMap)
	for _, endpointAdressMap := range endpointAddressMaps.UDPAddressMaps {
		addressmap := udpAddressMap[endpointAdressMap.Endpoint]
		if addressmap == nil {
			udpAddressMap[endpointAdressMap.Endpoint] = endpointAdressMap.AddressMap
			continue
		}

		for k, v := range endpointAdressMap.AddressMap {
			addressmap[k] = v
		}

		udpAddressMap[endpointAdressMap.Endpoint] = addressmap
	}

	return tcpAddressMap, udpAddressMap
}

func setupReverseServAddressMap(dir string) {
	path := path.Join(dir, "addressmap.json")
	tcpAddressMaps, udpAddressMaps := setupAddressMap(path)

	reverseServ, err := NewReverseServ(tcpAddressMaps, udpAddressMaps)
	if err != nil {
		log.Panicln("new udp reverse server failed:", err)
	}

	reverseServer = reverseServ

}

// CreateHTTPServer start http server
func CreateHTTPServer(listenAddr string, wsPath string, accountFilePath string) {
	setupBuiltinAccount(accountFilePath)
	setupReverseServAddressMap(path.Join(path.Dir(accountFilePath)))

	var err error
	dnsServerAddr, err = net.ResolveUDPAddr("udp", "8.8.8.8:53")
	if err != nil {
		log.Fatal("resolve dns server address failed:", err)
	}

	go keepalive()
	http.HandleFunc("/", indexHandler)
	http.HandleFunc(wsPath, wsHandler)
	log.Infof("server listen at:%s, path:%s", listenAddr, wsPath)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
