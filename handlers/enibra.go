// handlers/enibra.go
package handlers

import (
	"context"
	"crypto/tls"
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

/*
ENV beklenenler (NSSM -> Environment):
  ENIBRA_BASE_URL         = http://127.0.0.1:8088  |  https://127.0.0.1:8443
  ENIBRA_HOST_HEADER      = ik.hysavm.com.tr       (vhost/SNI için; yoksa boş bırak)
  ENIBRA_INSECURE_TLS     = 1                      (sadece iç ortam/öz-imzalı sertifika için)
  ENIBRA_MUSTERI_KODU     = HYS
  ENIBRA_PAROLA           = ****
  ENIBRA_TIMEOUT_MS       = 8000    (opsiyonel)
  ENIBRA_CACHE_SEC        = 30      (opsiyonel)
*/

// ===================== Enibra Client =====================

type enibraClient struct {
	base       string // örn: http://127.0.0.1:8088 veya https://127.0.0.1:8443
	hostHeader string // örn: ik.hysavm.com.tr (virtual host + SNI)
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
	base := strings.TrimRight(os.Getenv("ENIBRA_BASE_URL"), "/")
	host := os.Getenv("ENIBRA_HOST_HEADER")
	mus := os.Getenv("ENIBRA_MUSTERI_KODU")
	par := os.Getenv("ENIBRA_PAROLA")

	// timeout
	tout := 8 * time.Second
	if ms, _ := strconv.Atoi(os.Getenv("ENIBRA_TIMEOUT_MS")); ms > 0 {
		tout = time.Duration(ms) * time.Millisecond
	}

	// cache süresi
	ttl := 30 * time.Second
	if sec, _ := strconv.Atoi(os.Getenv("ENIBRA_CACHE_SEC")); sec > 0 {
		ttl = time.Duration(sec) * time.Second
	}

	// Transport (HTTPS ise TLS ayarları)
	tr := &http.Transport{}
	if strings.HasPrefix(strings.ToLower(base), "https://") {
		insecure := os.Getenv("ENIBRA_INSECURE_TLS") == "1"
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: insecure, // yalnızca iç ortamda kullanın
			ServerName:         host,     // 127.0.0.1'e bağlansak bile SNI=host
		}
	}

	return &enibraClient{
		base:       base,
		hostHeader: host,
		musteri:    mus,
		parola:     par,
		client:     &http.Client{Timeout: tout, Transport: tr},
		cache:      &enibraCache{ttl: ttl},
	}
}

func (c *enibraClient) personelListesi(ctx context.Context, extra url.Values) (status int, body []byte, contentType string, err error) {
	// cache key
	key := "PersonelListesi.doms?" + extra.Encode()
	if b, ct, s, ok := c.cacheGet(key); ok {
		return s, b, ct, nil
	}

	// query
	q := url.Values{}
	q.Set("MUSTERI_KODU", c.musteri)
	q.Set("PAROLA", c.parola)
	for k := range extra {
		for _, v := range extra[k] {
			q.Add(k, v)
		}
	}

	endpoint := c.base + "/PersonelListesi.doms?" + q.Encode()

	// request
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if hh := c.hostHeader; hh != "" {
		// virtual host + upstream header
		req.Host = hh
		req.Header.Set("Host", hh)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HYS-Backend/1.0")

	// send
	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("[enibra] request error: %v", err)
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json; charset=utf-8"
	}

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

// GET /api/enibra/personeller[?...]
// Mobil uygulama bu endpoint’ten direkt veri çeker.
func EnibraPersonelListesiProxy(w http.ResponseWriter, r *http.Request) {
	cli := newEnibraClientFromEnv()

	// istemcinin query’lerini geçir
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
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_error"})
		return
	}

	// Upstream beklenmedik şekilde HTML hata sayfası dönerse 502 verelim
	if strings.Contains(strings.ToLower(ct), "text/html") {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_error_html"})
		return
	}

	// Upstream ne döndüyse aynen geçir
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ===================== helpers =====================

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
