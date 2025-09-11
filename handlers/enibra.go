package handlers

import (
	"context"
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

type enibraClient struct {
	base    string
	musteri string
	parola  string
	client  *http.Client
	cache   *enibraCache
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
	base := os.Getenv("ENIBRA_BASE_URL")
	mus := os.Getenv("ENIBRA_MUSTERI_KODU")
	par := os.Getenv("ENIBRA_PAROLA")

	if base == "" || mus == "" || par == "" {
		log.Println("[enibra] Uyarı: ENIBRA_* env eksik. Lütfen .env ayarla.")
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
		base:    strings.TrimRight(base, "/"),
		musteri: mus,
		parola:  par,
		client:  &http.Client{Timeout: tout},
		cache:   &enibraCache{ttl: ttl},
	}
}

func (c *enibraClient) personelListesi(ctx context.Context, extra url.Values) (status int, body []byte, contentType string, err error) {
	// cache key = path + query
	key := "PersonelListesi?" + extra.Encode()
	if b, ct, s, ok := c.cacheGet(key); ok {
		return s, b, ct, nil
	}

	q := url.Values{}
	q.Set("MUSTERI_KODU", c.musteri)
	q.Set("PAROLA", c.parola)
	// İstemciden gelen ekstra query’leri de geçir (örn. sayfalama vs.)
	for k := range extra {
		for _, v := range extra[k] {
			q.Add(k, v)
		}
	}

	endpoint := c.base + "/PersonelListesi.doms?" + q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		// çoğu .doms JSON döner; garantiye al
		ct = "application/json; charset=utf-8"
	}

	// Başarılı/başarısız tüm yanıtları kısa süreli cache’le (thundering herd’i engeller)
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

// GET /api/enibra/personeller[?page=1&limit=100&...]
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
		http.Error(w, "enibra_upstream_error", http.StatusBadGateway)
		return
	}

	// Upstream ne döndüyse aynı status + content-type
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
