package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ===================== Enibra Client =====================

type enibraClient struct {
	base       string // örn: http://91.93.154.235:8088  (DNS bypass)
	hostHeader string // örn: ik.hysavm.com.tr          (virtual host)
	musteri    string
	parola     string
	client     *http.Client
	cache      *enibraCache
}

type enibraCache struct {
	ttl   time.Duration
	store sync.Map // key -> cachedItem
}

type cachedItem struct {
	expireAt time.Time
	body     []byte
	ct       string
	status   int
}

func newEnibraClientFromEnv() *enibraClient {
	base := os.Getenv("ENIBRA_BASE_URL")    // zorunlu
	host := os.Getenv("ENIBRA_HOST_HEADER") // opsiyonel (virtual host)
	mus := os.Getenv("ENIBRA_MUSTERI_KODU") // zorunlu
	par := os.Getenv("ENIBRA_PAROLA")       // zorunlu

	if base == "" || mus == "" || par == "" {
		log.Println("[enibra] Uyarı: ENIBRA_* env eksik (ENIBRA_BASE_URL / ENIBRA_MUSTERI_KODU / ENIBRA_PAROLA).")
	}

	tout := 8 * time.Second
	if ms, _ := strconv.Atoi(os.Getenv("ENIBRA_TIMEOUT_MS")); ms > 0 {
		tout = time.Duration(ms) * time.Millisecond
	}

	ttl := 30 * time.Second
	if sec, _ := strconv.Atoi(os.Getenv("ENIBRA_CACHE_SEC")); sec > 0 {
		ttl = time.Duration(sec) * time.Second
	}

	return &enibraClient{
		base:       strings.TrimRight(base, "/"),
		hostHeader: host,
		musteri:    mus,
		parola:     par,
		client:     &http.Client{Timeout: tout},
		cache:      &enibraCache{ttl: ttl},
	}
}

func (c *enibraClient) personelListesi(ctx context.Context, extra url.Values) (status int, body []byte, contentType string, err error) {
	// Cache key
	key := "PersonelListesi.doms?" + extra.Encode()
	if b, ct, s, ok := c.cacheGet(key); ok {
		return s, b, ct, nil
	}

	// Query birleştir
	q := url.Values{}
	q.Set("MUSTERI_KODU", c.musteri)
	q.Set("PAROLA", c.parola)
	for k := range extra {
		for _, v := range extra[k] {
			q.Add(k, v)
		}
	}

	endpoint := c.base + "/PersonelListesi.doms?" + q.Encode()

	// İstek hazırla
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)

	// Virtual host gerekiyorsa
	if hh := c.hostHeader; hh != "" {
		req.Host = hh
		req.Header.Set("Host", hh)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HYS-Backend/1.0")

	// Gönder
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json; charset=utf-8"
	}

	// Cache’e koy
	c.cacheSet(key, b, ct, resp.StatusCode)
	return resp.StatusCode, b, ct, nil
}

func (c *enibraClient) cacheGet(key string) (body []byte, ct string, status int, ok bool) {
	if v, ok := c.cache.store.Load(key); ok {
		item := v.(cachedItem)
		if time.Now().Before(item.expireAt) {
			return item.body, item.ct, item.status, true
		}
		c.cache.store.Delete(key)
	}
	return nil, "", 0, false
}

func (c *enibraClient) cacheSet(key string, body []byte, ct string, status int) {
	c.cache.store.Store(key, cachedItem{
		expireAt: time.Now().Add(c.cache.ttl),
		body:     body,
		ct:       ct,
		status:   status,
	})
}

// ===================== HTTP Handler =====================

// GET /api/enibra/personeller
func EnibraPersonelListesiProxy(w http.ResponseWriter, r *http.Request) {
	cli := newEnibraClientFromEnv()

	// İstemcinin query’lerini aynen geçir
	extra := url.Values{}
	for k, vals := range r.URL.Query() {
		for _, v := range vals {
			extra.Add(k, v)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), cli.client.Timeout)
	defer cancel()

	status, body, ct, err := cli.personelListesi(ctx, extra)
	if err != nil {
		log.Printf("[enibra] upstream error: %v", err)
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_error"})
		return
	}

	// Enibra bazen 200 + HTML hata sayfası döndürebilir → 502 dönelim
	if strings.Contains(strings.ToLower(ct), "text/html") {
		log.Printf("[enibra] unexpected HTML response from upstream, returning 502")
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_error_html"})
		return
	}

	// Upstream ne döndüyse aynen geçir
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ===================== küçük yardımcı =====================

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
