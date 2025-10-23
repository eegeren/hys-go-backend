// handlers/enibra.go
package handlers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
	store sync.Map
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
			InsecureSkipVerify: insecure,
			ServerName:         host,
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
// handlers/enibra.go içindeki EnibraPersonelDetay'ı bununla değiştir
func EnibraPersonelDetay(w http.ResponseWriter, r *http.Request) {
	tc := strings.TrimSpace(r.URL.Query().Get("tc"))
	if tc == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_tc"})
		return
	}

	cli := newEnibraClientFromEnv()
	if cli.base == "" || cli.musteri == "" || cli.parola == "" {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server_not_configured"})
		return
	}

	// DİKKAT: client timeout alanı "cli.client.Timeout"
	ctx, cancel := context.WithTimeout(r.Context(), cli.http.Timeout)
	defer cancel()

	// Tüm listeyi çek (cache'li)
	status, body, ct, err := cli.personelListesi(ctx, url.Values{})
	if err != nil || status < 200 || status >= 300 {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_error"})
		return
	}
	// HTML geldiyse (hata sayfası vb.)
	if strings.Contains(strings.ToLower(ct), "text/html") {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_error_html"})
		return
	}

	// Gelen JSON hem []map hem {items:[...]} hem de {SONUC_MESAJI:[...]} olabilir — hepsini destekle
	var (
		items []map[string]any
	)

	// 1) Dizi mi?
	if err := json.Unmarshal(body, &items); err != nil || len(items) == 0 {
		// 2) Nesne + items?
		var obj map[string]any
		if json.Unmarshal(body, &obj) == nil {
			if v, ok := obj["items"].([]any); ok && len(v) > 0 {
				tmp := make([]map[string]any, 0, len(v))
				for _, it := range v {
					if m, ok := it.(map[string]any); ok {
						tmp = append(tmp, m)
					}
				}
				items = tmp
			} else if v, ok := obj["SONUC_MESAJI"].([]any); ok && len(v) > 0 {
				// Enibra bazı uçlarda SONUC_MESAJI altında liste döndürüyor olabilir
				tmp := make([]map[string]any, 0, len(v))
				for _, it := range v {
					if m, ok := it.(map[string]any); ok {
						tmp = append(tmp, m)
					}
				}
				items = tmp
			}
		}
	}

	if len(items) == 0 {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "non_json_or_empty"})
		return
	}

	// TC ile kaydı bul
	row := func(list []map[string]any, want string) map[string]any {
		want = strings.TrimSpace(want)
		keys := []string{"TC_KIMLIK_NO", "TC", "TC_NO", "tc", "tckimlik"}
		for _, it := range list {
			for _, k := range keys {
				if v, ok := it[k]; ok {
					if strings.TrimSpace(asStr(v)) == want {
						return it
					}
				}
			}
		}
		return nil
	}(items, tc)

	if row == nil {
		respondJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}

	// Normalize alanlar
	pickStr := func(m map[string]any, keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				return strings.TrimSpace(asStr(v))
			}
		}
		return ""
	}
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		r := strings.NewReplacer("ğ", "g", "ü", "u", "ş", "s", "ı", "i", "ö", "o", "ç", "c")
		return r.Replace(s)
	}

	ad := pickStr(row, "ADI", "AD", "ad")
	soyad := pickStr(row, "SOYADI", "SOYAD", "soyad")
	tcOut := pickStr(row, "TC_KIMLIK_NO", "TC", "TC_NO", "tc", "tckimlik")

	// Şube / görev yeri ismi
	subeAdi := pickStr(row, "SUBE", "GOREV_YERI", "ISYERI", "ISYERI_ADI", "sube")
	ham := norm(subeAdi + " " + pickStr(row, "ISYERI_TIPI", "BOLUM", "DEPARTMAN"))

	konum := "BILINMIYOR"
	switch {
	case strings.Contains(ham, "genel") || strings.Contains(ham, "merkez") || strings.Contains(ham, "gm"):
		konum = "GENEL_MERKEZ"
	case strings.Contains(ham, "magaza") || strings.Contains(ham, "mağaza") ||
		strings.Contains(ham, "satis") || strings.Contains(ham, "satış"):
		konum = "MAGAZA"
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"tc":         tcOut,
		"ad":         ad,
		"soyad":      soyad,
		"sube_adi":   subeAdi,
		"konum_tipi": konum, // "GENEL_MERKEZ" | "MAGAZA" | "BILINMIYOR"
	})
}

// ===================== Shift warnings =====================

// GET /api/enibra/vardiya-uyarilari
// Vardiyası belirli bir saatte başlayıp kart basmamış (GIRIS_SAATI boş) personelleri listeler.
// Varsayılan kontrol saati now(), tolerans (grace) 20 dakikadır.
func EnibraVardiyaUyarilari(w http.ResponseWriter, r *http.Request) {
	checkAt := time.Now().In(time.Local)
	if v := strings.TrimSpace(r.URL.Query().Get("check_time")); v != "" {
		parsed, err := parseFlexibleTime(v, checkAt)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_check_time"})
			return
		}
		checkAt = parsed
	}

	grace := 20
	if v := strings.TrimSpace(r.URL.Query().Get("grace_min")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grace_minutes"})
			return
		}
		grace = n
	}

	targetStart := checkAt.Add(-time.Duration(grace) * time.Minute)
	wantHour, wantMinute := targetStart.Hour(), targetStart.Minute()

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
	if strings.Contains(strings.ToLower(ct), "text/html") {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_error_html"})
		return
	}

	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) == 0 {
		var wrapper struct {
			SonucMesaji []map[string]any `json:"SONUC_MESAJI"`
		}
		if err := json.Unmarshal(body, &wrapper); err != nil {
			respondJSON(w, http.StatusBadGateway, map[string]any{"error": "invalid_upstream_json"})
			return
		}
		rows = wrapper.SonucMesaji
	}
	if len(rows) == 0 {
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": "empty_upstream"})
		return
	}

	missing := make([]map[string]any, 0)
	for _, rec := range rows {
		startVal := strings.TrimSpace(anyToString(rec["VARDIYA_BASLANGIC"]))
		hour, minute, ok := extractHourMinute(startVal)
		if !ok || hour != wantHour || minute != wantMinute {
			continue
		}

		giris := strings.TrimSpace(anyToString(rec["GIRIS_SAATI"]))
		if giris != "" {
			continue
		}

		missing = append(missing, map[string]any{
			"tc":                strings.TrimSpace(anyToString(firstNonEmpty(rec, "TC_KIMLIK_NO", "TC", "TC_NO", "tc", "tckimlik"))),
			"ad":                strings.TrimSpace(anyToString(firstNonEmpty(rec, "ADI", "AD", "ad"))),
			"soyad":             strings.TrimSpace(anyToString(firstNonEmpty(rec, "SOYADI", "SOYAD", "soyad"))),
			"vardiya_baslangic": startVal,
			"giris_saati":       giris,
		})
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"check_time":          checkAt.Format(time.RFC3339),
		"grace_minutes":       grace,
		"target_shift_hour":   fmt.Sprintf("%02d:%02d", wantHour, wantMinute),
		"missing_entry_count": len(missing),
		"items":               missing,
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

// asStr: interface{} → string normalize
func asStr(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		// float -> string (exponent olmadan)
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int, int32, int64:
		return fmt.Sprint(t)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

var (
	clockRegex  = regexp.MustCompile(`(\d{1,2})[:.](\d{2})`)
	timeLayouts = []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"02.01.2006 15:04:05",
		"02.01.2006 15:04",
		"15:04:05",
		"15:04",
	}
)

func extractHourMinute(s string) (int, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}

	loc := time.Local
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.Hour(), t.Minute(), true
		}
	}

	if match := clockRegex.FindStringSubmatch(s); len(match) == 3 {
		hour, err1 := strconv.Atoi(match[1])
		min, err2 := strconv.Atoi(match[2])
		if err1 == nil && err2 == nil && hour >= 0 && hour < 24 && min >= 0 && min < 60 {
			return hour, min, true
		}
	}
	return 0, 0, false
}

func parseFlexibleTime(val string, base time.Time) (time.Time, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return base, nil
	}

	loc := base.Location()
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, val, loc); err == nil {
			if layout == "15:04" || layout == "15:04:05" {
				return time.Date(base.Year(), base.Month(), base.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc), nil
			}
			return t, nil
		}
	}

	if match := clockRegex.FindStringSubmatch(val); len(match) == 3 {
		hour, err1 := strconv.Atoi(match[1])
		min, err2 := strconv.Atoi(match[2])
		if err1 == nil && err2 == nil && hour >= 0 && hour < 24 && min >= 0 && min < 60 {
			return time.Date(base.Year(), base.Month(), base.Day(), hour, min, 0, 0, loc), nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse time")
}

func firstNonEmpty(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s := strings.TrimSpace(anyToString(v)); s != "" {
				return v
			}
		}
	}
	return ""
}
