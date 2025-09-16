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

// ===================== Client & Cache =====================

type enibraClient struct {
	base       string
	hostHeader string
	musteri    string
	parola     string
	http       *http.Client
	cache      *enibraCache
}

type enibraCache struct {
	ttl   time.Duration
	store sync.Map // key -> cachedItem
}

type cachedItem struct {
	expireAt time.Time
	status   int
	ct       string
	body     []byte
}

func newEnibraClientFromEnv() *enibraClient {
	base := strings.TrimRight(os.Getenv("ENIBRA_BASE_URL"), "/")
	mus := os.Getenv("ENIBRA_MUSTERI_KODU")
	par := os.Getenv("ENIBRA_PAROLA")
	host := os.Getenv("ENIBRA_HOST_HEADER")

	// timeout
	tout := 10 * time.Second
	if ms, _ := strconv.Atoi(os.Getenv("ENIBRA_TIMEOUT_MS")); ms > 0 {
		tout = time.Duration(ms) * time.Millisecond
	}

	// cache ttl
	ttl := 30 * time.Second
	if sec, _ := strconv.Atoi(os.Getenv("ENIBRA_CACHE_SEC")); sec > 0 {
		ttl = time.Duration(sec) * time.Second
	}

	tr := &http.Transport{}
	if strings.HasPrefix(strings.ToLower(base), "https://") {
		insecure := os.Getenv("ENIBRA_INSECURE_TLS") == "1"
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: insecure, // self-signed vs.
			ServerName:         host,     // SNI/Host override gerekirse
		}
	}

	return &enibraClient{
		base:       base,
		hostHeader: host,
		musteri:    mus,
		parola:     par,
		http:       &http.Client{Timeout: tout, Transport: tr},
		cache:      &enibraCache{ttl: ttl},
	}
}

func (c *enibraClient) personelListesi(ctx context.Context, extra url.Values) (int, []byte, string, error) {
	// cache key = path + query
	key := "PersonelListesi.doms?" + extra.Encode()
	if b, ct, st, ok := c.cacheGet(key); ok {
		return st, b, ct, nil
	}

	q := url.Values{}
	q.Set("MUSTERI_KODU", c.musteri)
	q.Set("PAROLA", c.parola)
	for k, vals := range extra {
		for _, v := range vals {
			q.Add(k, v)
		}
	}
	endpoint := c.base + "/PersonelListesi.doms?" + q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HYS-Backend/1.0")
	if c.hostHeader != "" {
		req.Host = c.hostHeader
		req.Header.Set("Host", c.hostHeader)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json; charset=utf-8"
	}

	c.cacheSet(key, body, ct, resp.StatusCode)
	return resp.StatusCode, body, ct, nil
}

func (c *enibraClient) cacheGet(key string) ([]byte, string, int, bool) {
	if v, ok := c.cache.store.Load(key); ok {
		it := v.(cachedItem)
		if time.Now().Before(it.expireAt) {
			return it.body, it.ct, it.status, true
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

// ===================== RAW PROXY =====================

// GET /api/enibra/personeller
// Upstream ne dönerse aynen geçirir (JSON/CT vs. korunur)
func EnibraPersonelListesiProxy(w http.ResponseWriter, r *http.Request) {
	cli := newEnibraClientFromEnv()
	if cli.base == "" || cli.musteri == "" || cli.parola == "" {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server_not_configured"})
		return
	}

	// Query’yi aynen iletelim (page/limit/q gibi kendi parametrelerini de iletebilirsin)
	extra := url.Values{}
	for k, vals := range r.URL.Query() {
		for _, v := range vals {
			extra.Add(k, v)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), cli.http.Timeout)
	defer cancel()

	status, body, ct, err := cli.personelListesi(ctx, extra)
	if err != nil {
		log.Printf("[enibra] upstream error: %v", err)
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_error"})
		return
	}

	// HTML hata sayfası gelirse 502 verelim
	if strings.Contains(strings.ToLower(ct), "text/html") {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_error_html"})
		return
	}

	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ===================== NORMALIZE JSON =====================

// GET /api/enibra/detay
// Upstream JSON’unu sadeleştirir + arama/sayfalama uygular
func EnibraPersonelDetay(w http.ResponseWriter, r *http.Request) {
	cli := newEnibraClientFromEnv()
	if cli.base == "" || cli.musteri == "" || cli.parola == "" {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server_not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cli.http.Timeout)
	defer cancel()

	status, body, ct, err := cli.personelListesi(ctx, url.Values{})
	if err != nil || status < 200 || status >= 300 {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_error"})
		return
	}

	// Beklenen şema: {"SONUC_KODU":0, "SONUC_MESAJI":[ {...}, {...} ]}
	var root struct {
		SonucKodu   any              `json:"SONUC_KODU"`
		SonucMesaji []map[string]any `json:"SONUC_MESAJI"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "invalid_upstream_json"})
		return
	}

	type Person struct {
		InsanID string `json:"insan_id"`
		TC      string `json:"tc"`
		Ad      string `json:"ad"`
		Soyad   string `json:"soyad"`
		Gorev   string `json:"gorev"`
		Unvan   string `json:"unvan"`
		Sube    string `json:"sube"`
		Telefon string `json:"telefon"`
	}

	getStr := func(m map[string]any, keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				switch t := v.(type) {
				case string:
					return strings.TrimSpace(t)
				case float64:
					if t == float64(int64(t)) {
						return strconv.FormatInt(int64(t), 10)
					}
					return strconv.FormatFloat(t, 'f', -1, 64)
				default:
					return strings.TrimSpace(anyToString(v))
				}
			}
		}
		return ""
	}

	all := make([]Person, 0, len(root.SonucMesaji))
	for _, m := range root.SonucMesaji {
		all = append(all, Person{
			InsanID: getStr(m, "INSAN_ID", "INSANID", "ID"),
			TC:      getStr(m, "TC_KIMLIK_NO", "TC", "TCKN", "TC_NO"),
			Ad:      getStr(m, "ADI", "AD"),
			Soyad:   getStr(m, "SOYADI", "SOYAD"),
			Gorev:   getStr(m, "GOREV", "GOREVI"),
			Unvan:   getStr(m, "UNVAN"),
			Sube:    getStr(m, "GOREV_YERI", "SUBE", "ISYERI"),
			Telefon: getStr(m, "TELEFON", "CEP_TEL", "GSM"),
		})
	}

	// q: basit arama
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filtered := all
	if q != "" {
		filtered = filtered[:0]
		for _, p := range all {
			hay := strings.ToLower(p.Ad + " " + p.Soyad + " " + p.Gorev + " " + p.Unvan + " " + p.Sube + " " + p.TC)
			if strings.Contains(hay, q) {
				filtered = append(filtered, p)
			}
		}
	}

	// page/limit
	page := 1
	limit := 200
	maxLimit := 2000
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxLimit {
				n = maxLimit
			}
			limit = n
		}
	}

	total := len(filtered)
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	pageItems := filtered[start:end]

	respondJSON(w, http.StatusOK, map[string]any{
		"page":  page,
		"limit": limit,
		"total": total,
		"items": pageItems,
		"ct":    ct, // debug amaçlı
	})
}

// ===================== Single record by TC =====================

// GET /api/enibra/personel?tc=XXXXXXXXXXX
func EnibraPersonelByTC(w http.ResponseWriter, r *http.Request) {
	tc := strings.TrimSpace(r.URL.Query().Get("tc"))
	if tc == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_tc"})
		return
	}

	// normalize endpointini kullanıp TC filtreliyoruz (performans: cache aktif)
	cli := newEnibraClientFromEnv()
	if cli.base == "" || cli.musteri == "" || cli.parola == "" {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server_not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cli.http.Timeout)
	defer cancel()

	status, body, _, err := cli.personelListesi(ctx, url.Values{})
	if err != nil || status < 200 || status >= 300 {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_error"})
		return
	}

	var root struct {
		SonucMesaji []map[string]any `json:"SONUC_MESAJI"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "invalid_upstream_json"})
		return
	}

	// tüm bilinen TC alan isimleri
	keys := []string{"TC_KIMLIK_NO", "TC", "TCKN", "TC_NO", "tc", "tckimlik"}
	for _, m := range root.SonucMesaji {
		for _, k := range keys {
			if as := strings.TrimSpace(anyToString(m[k])); as != "" && as == tc {
				respondJSON(w, http.StatusOK, m)
				return
			}
		}
	}
	respondJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
}

// ===================== helpers =====================

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func anyToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(toStringSlow(v), "\r", ""), "\n", ""))
	}
}

// toStringSlow: fmt.Sprintf fallback'ı; import fmt eklememek için küçük helper
func toStringSlow(v any) string {
	type stringer interface{ String() string }
	if s, ok := v.(stringer); ok {
		return s.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}
