package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

var (
	whitelistLock      sync.Mutex
	whitelistStrings   string
	whitelistGenerated bool
)

func generateWhitelist() error {
	rsp, err := http.Get("https://raw.githubusercontent.com/felixonmars/dnsmasq-china-list/refs/heads/master/accelerated-domains.china.conf")
	if err != nil {
		return err
	}

	defer rsp.Body.Close()

	if rsp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(rsp.Body)
		if err != nil {
			return err
		}

		bodyString := string(bodyBytes)
		whitelist := make(map[string]struct{})
		reader := bufio.NewReader(strings.NewReader(bodyString))
		for {
			linebytes, isPrefix, err := reader.ReadLine()
			if err != nil {
				if err == io.EOF {
					break
				}

				return err
			}

			if isPrefix {
				return fmt.Errorf("generate whitelist failed, underlying buffer is too small")
			}

			domain := strings.TrimSpace(string(linebytes))
			xxx := strings.Split(domain, "/")
			if len(xxx) != 3 {
				log.Errorf("generate whitelist error, %s not expected format", domain)
				continue
			}

			domain = xxx[1]
			if len(domain) > 0 {
				whitelist[domain] = struct{}{}
			}
		}

		whitelist["cn"] = struct{}{}
		log.Infof("generate whitelist domain name count: %d", len(whitelist))

		var sb strings.Builder
		for k := range whitelist {
			sb.WriteString(k)
			sb.WriteString("\n")
		}

		whitelistStrings = sb.String()
		whitelistGenerated = true
		return nil
	} else {
		return fmt.Errorf("generate whitelist rsp status code %d != 200", rsp.StatusCode)
	}
}

func whitelistHandler(w http.ResponseWriter, r *http.Request) {
	whitelistLock.Lock()
	defer whitelistLock.Unlock()

	if whitelistGenerated {
		w.Write([]byte(whitelistStrings))
	} else {
		err := generateWhitelist()
		if err != nil {
			log.Errorf("generate whitelist failed:%s", err)
			w.Write([]byte("cn"))
		} else {
			w.Write([]byte(whitelistStrings))
		}
	}
}
