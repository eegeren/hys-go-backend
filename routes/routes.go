// handlers/enibra.go
package handlers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ---- PUBLIC HANDLERS ----

// GET /api/enibra/personeller   -> mevcut listenizi olduğu gibi proxylıyor (siz de var)
// GET /api/enibra/personel?tc=XXXXXXXXXXX
func EnibraPersonelByTC(w http.ResponseWriter, r *http.Request) {
	tc := strings.TrimSpace(r.URL.Query().Get("tc"))
	if tc == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_tc"})
		return
	}

	conf := loadEnibraConf()
	if err := conf.validate(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server_not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()

	items, status, err := fetchEnibraList(ctx, conf) // tüm listeyi çek
	if err != nil {
		// upstream hatasını kullanıcıya taşımayalım
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_error"})
		return
	}
	if status < 200 || status >= 300 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "enibra_upstream_status", "status": status})
		return
	}

	// TC alanı farklı isimlerle gelebilir, hepsini dene
	match := findByTC(items, tc)
	if match == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}

	// tek kaydı döndür
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(match)
}

// (Var olan fonksiyonunuz kalsın) — isterse burada da dursun:
// func EnibraPersonelListesiProxy(w http.ResponseWriter, r *http.Request) { ... }

// ---- INTERNALS ----

type enibraConf struct {
	BaseURL    string // ENIBRA_BASE_URL (örn: http://ik.hysavm.com.tr:8088)
	Musteri    string // ENIBRA_MUSTERI_KODU
	Parola     string // ENIBRA_PAROLA
	HostHeader string // ENIBRA_HOST_HEADER (opsiyonel)
	Insecure   bool   // ENIBRA_INSECURE_TLS=1 -> self-signed vs.
}

func loadEnibraConf() enibraConf {
	ins := strings.TrimSpace(os.Getenv("ENIBRA_INSECURE_TLS")) == "1"
	return enibraConf{
		BaseURL:    strings.TrimRight(os.Getenv("ENIBRA_BASE_URL"), "/"),
		Musteri:    os.Getenv("ENIBRA_MUSTERI_KODU"),
		Parola:     os.Getenv("ENIBRA_PAROLA"),
		HostHeader: os.Getenv("ENIBRA_HOST_HEADER"),
		Insecure:   ins,
	}
}

func (c enibraConf) validate() error {
	if c.BaseURL == "" || c.Musteri == "" || c.Parola == "" {
		return errors.New("missing enibra env")
	}
	return nil
}

func httpClient(insecure bool) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec
	}
	return &http.Client{
		Timeout:   35 * time.Second,
		Transport: tr,
	}
}

func fetchEnibraList(ctx context.Context, conf enibraConf) ([]map[string]any, int, error) {
	// En yaygın uç: PersonelListesi.doms
	u, _ := url.Parse(conf.BaseURL + "/PersonelListesi.doms")
	q := u.Query()
	q.Set("MUSTERI_KODU", conf.Musteri)
	q.Set("PAROLA", conf.Parola)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("Accept", "application/json")
	if conf.HostHeader != "" {
		req.Host = conf.HostHeader
	}

	resp, err := httpClient(conf.Insecure).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	// JSON dizi
	var arr []map[string]any
	if json.Unmarshal(b, &arr) == nil {
		return arr, resp.StatusCode, nil
	}

	// items içinde gelebilir
	var obj map[string]any
	if json.Unmarshal(b, &obj) == nil {
		if v, ok := obj["items"].([]any); ok {
			out := make([]map[string]any, 0, len(v))
			for _, it := range v {
				if m, ok := it.(map[string]any); ok {
					out = append(out, m)
				}
			}
			return out, resp.StatusCode, nil
		}
	}

	// JSON değilse (HTML hata vs.)
	return nil, resp.StatusCode, errors.New("non-json")
}

func findByTC(items []map[string]any, tc string) map[string]any {
	keys := []string{"TC_KIMLIK_NO", "TC", "TC_NO", "tc", "tckimlik"}

	for _, it := range items {
		for _, k := range keys {
			if v, ok := it[k]; ok {
				if asStr(v) == tc {
					return it
				}
			}
		}
	}
	return nil
}

func asStr(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(strings.TrimRight(strings.TrimRight(strings.TrimRight(strings.FormatFloat(t, 'f', -1, 64), "0"), "."), "0"), "."), "0"), "."))
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll("%!v(MISSING)", "%!(EXTRA", ""), "%!(N", ""), "\n", ""), "\t", ""), "  ", " ")), "\r", ""))
	}
}