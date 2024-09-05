package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"net/http"
)

var (
	upgrader      = websocket.Upgrader{} // use default options
	accountMap    = make(map[string]*Account)
	dnsServerAddr *net.UDPAddr

	whitelistA *Whitelist = nil
	whitelistB *Whitelist = nil

	udpServ *UDPServ = nil
)

type Whitelist struct {
	domainsData []byte
	version     int
}

type AddressMap map[string]string

func wsHandler(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()

	var uuid = r.URL.Query().Get("uuid")
	if uuid == "" {
		log.Println("need uuid!")
		return
	}

	var endpoint = r.URL.Query().Get("endpoint")
	if endpoint == "" {
		log.Println("need endpoint!")
		return
	}

	account, ok := accountMap[uuid]
	if !ok {
		log.Println("no account found for uuid:", uuid)
		return
	}

	account.acceptWebsocket(c, udpServ, endpoint)
}

// indexHandler responds to requests with our greeting.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	fmt.Fprint(w, "Hello, Stupid!")
}

func whitelistHandler(w http.ResponseWriter, r *http.Request) {
	handled := false
	defer func() {
		if !handled {
			http.NotFound(w, r)
			return
		}
	}()

	// query string: version
	query := r.URL.Query()
	versionStr := query.Get("version")
	uuid := query.Get("uuid")
	var whitelist *Whitelist = whitelistA

	if uuid != "" {
		account, ok := accountMap[uuid]
		if ok {
			if !account.useWhitelistA {
				whitelist = whitelistB
			}
		}
	}

	if whitelist == nil {
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return
	}

	if whitelist.version <= version {
		return
	}

	w.Write(whitelist.domainsData)
	handled = true
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
	fileBytes, err := ioutil.ReadFile(accountFilePath)
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

	log.Println("load account ok, number of account:", len(accountCfgs.Accounts))
}

func setupWhitelist(whitelistFilePath string) *Whitelist {
	fileBytes, err := ioutil.ReadFile(whitelistFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		log.Panicln("read domain cfg file failed:", err)
	}

	type jsonstruct struct {
		Version int `json:"version"`
	}

	var domains = &jsonstruct{}
	err = json.Unmarshal(fileBytes, domains)
	if err != nil {
		log.Panicln("parse domain cfg file failed:", err)
	}

	whitelist := &Whitelist{
		version:     domains.Version,
		domainsData: fileBytes,
	}

	log.Printf("load domain ok, path:%s, version:%d", whitelistFilePath, whitelist.version)

	return whitelist
}

func setupWhitelists(dir string) {
	path1 := path.Join(dir, "whitelist-a.json")
	path2 := path.Join(dir, "whitelist-b.json")

	whitelistA = setupWhitelist(path1)
	whitelistB = setupWhitelist(path2)
}

func setupAddressMap(addressMapFilePath string) map[string]AddressMap {
	fileBytes, err := ioutil.ReadFile(addressMapFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		log.Panicln("read addressmap cfg file failed:", err)
	}

	type jsonstruct struct {
		AddressMaps map[string]AddressMap `json:"udpmap"`
	}
	var addressMap = jsonstruct{}
	err = json.Unmarshal(fileBytes, &addressMap)
	if err != nil {
		log.Panicln("parse addressmap cfg file failed:", err)
	}

	return addressMap.AddressMaps
}

func setupUdpAddressMap(dir string) {
	path := path.Join(dir, "addressmap.json")
	addressMaps := setupAddressMap(path)

	udpRevSers, err := newUDPServ(addressMaps)
	if err != nil {
		log.Panicln("new udp reverse server failed:", err)
	}

	udpServ = udpRevSers
	// for _, account := range accountMap {
	// 	account.setUDPReverseServ(udpRevSers)
	// }

}

// CreateHTTPServer start http server
func CreateHTTPServer(listenAddr string, wsPath string, accountFilePath string) {
	setupBuiltinAccount(accountFilePath)
	setupWhitelists(path.Join(path.Dir(accountFilePath)))
	setupUdpAddressMap(path.Join(path.Dir(accountFilePath)))

	var err error
	dnsServerAddr, err = net.ResolveUDPAddr("udp", "8.8.8.8:53")
	if err != nil {
		log.Fatal("resolve dns server address failed:", err)
	}

	go keepalive()
	http.HandleFunc("/", indexHandler)
	http.HandleFunc(wsPath, wsHandler)
	http.HandleFunc(wsPath+"110", whitelistHandler)
	log.Printf("server listen at:%s, path:%s", listenAddr, wsPath)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
