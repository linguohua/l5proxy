package server

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	defaultWhitelistURL      = "https://raw.githubusercontent.com/felixonmars/dnsmasq-china-list/refs/heads/master/accelerated-domains.china.conf"
	whitelistRefreshInterval = 6 * time.Hour
	whitelistRetryCooldown   = 5 * time.Minute
)

var (
	whitelistLock        sync.Mutex
	whitelistFetchLock   sync.Mutex
	whitelistStrings     string
	whitelistUpdatedAt   time.Time
	whitelistNextRetryAt time.Time
	whitelistURL         = defaultWhitelistURL
	whitelistNow         = time.Now
)

func shouldUseCachedWhitelist(now time.Time) (string, bool) {
	whitelistLock.Lock()
	defer whitelistLock.Unlock()
	if whitelistStrings == "" {
		return "", false
	}
	if now.Sub(whitelistUpdatedAt) < whitelistRefreshInterval || now.Before(whitelistNextRetryAt) {
		return whitelistStrings, true
	}
	return whitelistStrings, false
}

func parseWhitelist(r io.Reader) (string, int, error) {
	whitelist := make(map[string]struct{})
	reader := bufio.NewReader(r)
	for {
		linebytes, isPrefix, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", 0, err
		}

		if isPrefix {
			return "", 0, fmt.Errorf("generate whitelist failed, underlying buffer is too small")
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

	var sb strings.Builder
	keys := make([]string, 0, len(whitelist))
	for k := range whitelist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString("\n")
	}

	return sb.String(), len(whitelist), nil
}

func fetchWhitelist() (string, error) {
	rsp, err := (&http.Client{Timeout: 10 * time.Second}).Get(whitelistURL)
	if err != nil {
		return "", err
	}
	defer rsp.Body.Close()

	if rsp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("generate whitelist rsp status code %d != 200", rsp.StatusCode)
	}

	result, count, err := parseWhitelist(rsp.Body)
	if err != nil {
		return "", err
	}
	log.Infof("generate whitelist domain name count: %d", count)
	return result, nil
}

func generateWhitelist() error {
	if _, ok := shouldUseCachedWhitelist(whitelistNow()); ok {
		return nil
	}

	whitelistFetchLock.Lock()
	defer whitelistFetchLock.Unlock()

	cached, ok := shouldUseCachedWhitelist(whitelistNow())
	if ok {
		return nil
	}

	result, err := fetchWhitelist()
	if err != nil {
		if cached != "" {
			whitelistLock.Lock()
			whitelistNextRetryAt = whitelistNow().Add(whitelistRetryCooldown)
			whitelistLock.Unlock()
			log.Warnf("refresh whitelist failed, using cached whitelist: %v", err)
			return nil
		}
		return err
	}

	whitelistLock.Lock()
	whitelistStrings = result
	whitelistUpdatedAt = whitelistNow()
	whitelistNextRetryAt = time.Time{}
	whitelistLock.Unlock()
	return nil
}

func whitelistHandler(w http.ResponseWriter, r *http.Request) {
	if err := generateWhitelist(); err != nil {
		log.Errorf("generate whitelist failed:%s", err)
		http.Error(w, "Whitelist unavailable", http.StatusServiceUnavailable)
		return
	}
	whitelistLock.Lock()
	result := whitelistStrings
	whitelistLock.Unlock()
	if result == "" {
		http.Error(w, "Whitelist unavailable", http.StatusServiceUnavailable)
		return
	}
	serveWhitelist(w, r, result)
}

func serveWhitelist(w http.ResponseWriter, r *http.Request, result string) {
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256([]byte(result)))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Write([]byte(result))
}
