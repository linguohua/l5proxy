package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	whitelistLock      sync.Mutex
	whitelistFetchLock sync.Mutex
	whitelistStrings   string
	whitelistGenerated bool
)

func generateWhitelist() error {
	whitelistFetchLock.Lock()
	defer whitelistFetchLock.Unlock()
	whitelistLock.Lock()
	ready := whitelistGenerated
	whitelistLock.Unlock()
	if ready {
		return nil
	}
	rsp, err := (&http.Client{Timeout: 10 * time.Second}).Get("https://raw.githubusercontent.com/felixonmars/dnsmasq-china-list/refs/heads/master/accelerated-domains.china.conf")
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
			if len(domain) > 0 && domain[0] == '#' {
				// skip comments
				continue
			}

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

		whitelistLock.Lock()
		whitelistStrings = sb.String()
		whitelistGenerated = true
		whitelistLock.Unlock()
		return nil
	} else {
		return fmt.Errorf("generate whitelist rsp status code %d != 200", rsp.StatusCode)
	}
}

func whitelistHandler(w http.ResponseWriter, r *http.Request) {
	if err := generateWhitelist(); err != nil {
		log.Errorf("generate whitelist failed:%s", err)
	}
	whitelistLock.Lock()
	result := whitelistStrings
	whitelistLock.Unlock()
	if result == "" {
		result = "cn"
	}
	w.Write([]byte(result))
}
