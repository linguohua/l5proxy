package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func resetWhitelistState() {
	whitelistLock.Lock()
	whitelistStrings = ""
	whitelistUpdatedAt = time.Time{}
	whitelistNextRetryAt = time.Time{}
	whitelistLock.Unlock()
	whitelistURL = defaultWhitelistURL
	whitelistNow = time.Now
}

func TestWhitelistConditional(t *testing.T) {
	r := httptest.NewRequest("GET", "/whitelist", nil)
	w := httptest.NewRecorder()
	serveWhitelist(w, r, "cn\nexample.com\n")
	tag := w.Header().Get("ETag")
	if w.Code != 200 || len(tag) != 66 {
		t.Fatal(w.Code, tag)
	}
	for _, v := range []string{tag, "W/" + tag, "\"old\", " + tag} {
		r.Header.Set("If-None-Match", v)
		w = httptest.NewRecorder()
		serveWhitelist(w, r, "cn\nexample.com\n")
		if w.Code != 304 || w.Body.Len() != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	serveWhitelist(w, r, "cn\nnew.com\n")
	if w.Code != 200 || w.Header().Get("ETag") == tag {
		t.Fatal("changed list not sent")
	}
}

func TestWhitelistRefreshAndFallback(t *testing.T) {
	t.Cleanup(resetWhitelistState)
	resetWhitelistState()

	var fetchCount atomic.Int32
	var failFetch atomic.Bool
	var payload atomic.Value
	payload.Store("server=/example.com/114.114.114.114\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		if failFetch.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(payload.Load().(string)))
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	whitelistURL = srv.URL
	whitelistNow = func() time.Time { return now }

	// 1. Cold start loads initial whitelist.
	if err := generateWhitelist(); err != nil {
		t.Fatalf("initial generateWhitelist failed: %v", err)
	}
	if fetchCount.Load() != 1 || whitelistStrings != "cn\nexample.com\n" {
		t.Fatalf("unexpected initial state: count=%d strings=%q", fetchCount.Load(), whitelistStrings)
	}

	// 2. Within 6 hours, cached whitelist is reused without fetching.
	now = now.Add(5*time.Hour + 59*time.Minute)
	if err := generateWhitelist(); err != nil {
		t.Fatalf("cached generateWhitelist failed: %v", err)
	}
	if fetchCount.Load() != 1 {
		t.Fatalf("expected no fetch before 6h, got %d", fetchCount.Load())
	}

	// 3. After 6 hours, refresh runs and updates whitelist.
	payload.Store("server=/updated.cn/114.114.114.114\n")
	now = now.Add(2 * time.Minute)
	if err := generateWhitelist(); err != nil {
		t.Fatalf("refresh generateWhitelist failed: %v", err)
	}
	if fetchCount.Load() != 2 || whitelistStrings != "cn\nupdated.cn\n" {
		t.Fatalf("unexpected refreshed state: count=%d strings=%q", fetchCount.Load(), whitelistStrings)
	}

	// 4. After another 6 hours, upstream failure keeps cached whitelist and respects retry cooldown.
	failFetch.Store(true)
	now = now.Add(6 * time.Hour)
	if err := generateWhitelist(); err != nil {
		t.Fatalf("expected stale cache fallback on refresh error, got: %v", err)
	}
	if fetchCount.Load() != 3 || whitelistStrings != "cn\nupdated.cn\n" {
		t.Fatalf("unexpected fallback state: count=%d strings=%q", fetchCount.Load(), whitelistStrings)
	}

	now = now.Add(2 * time.Minute)
	if err := generateWhitelist(); err != nil {
		t.Fatalf("cooldown generateWhitelist failed: %v", err)
	}
	if fetchCount.Load() != 3 {
		t.Fatalf("expected cooldown to skip fetch, got %d", fetchCount.Load())
	}

	// 5. After retry cooldown passes and upstream recovers, whitelist updates.
	failFetch.Store(false)
	payload.Store("server=/recovered.cn/114.114.114.114\n")
	now = now.Add(4 * time.Minute)
	if err := generateWhitelist(); err != nil {
		t.Fatalf("recovery generateWhitelist failed: %v", err)
	}
	if fetchCount.Load() != 4 || whitelistStrings != "cn\nrecovered.cn\n" {
		t.Fatalf("unexpected recovered state: count=%d strings=%q", fetchCount.Load(), whitelistStrings)
	}
}
