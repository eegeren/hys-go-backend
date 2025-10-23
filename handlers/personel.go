package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"time"
)

const enibraURL = "http://b2c.hysavm.com.tr:4500/api/enibra/personel"

func fetchEnibra(verbose bool) (status int, body []byte, finalURL string, hdr http.Header, err error) {
	transport := &http.Transport{
		Proxy:              http.ProxyFromEnvironment,
		DisableCompression: true,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 10 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     10 * time.Second,
		ForceAttemptHTTP2:   false,
	}

	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if verbose {
				log.Printf("↪️  Redirect: %s\n", req.URL.String())
			}
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, enibraURL, nil)
	if err != nil {
		return 0, nil, "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124 Safari/537.36")
	req.Header.Set("Accept", "application/json, application/xml, text/xml;q=0.9, */*;q=0.8")
	req.Header.Set("Accept-Language", "tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Connection", "close")
	req.Header.Set("Referer", "http://ik.hysavm.com.tr/")
	req.Host = "ik.hysavm.com.tr"

	if verbose {
		trace := &httptrace.ClientTrace{
			DNSStart: func(info httptrace.DNSStartInfo) { log.Printf("🔎 DNS start: %v", info.Host) },
			DNSDone:  func(info httptrace.DNSDoneInfo) { log.Printf("🔎 DNS done: %v (err=%v)", info.Addrs, info.Err) },
			ConnectStart: func(network, addr string) {
				log.Printf("🔌 Connect start: %s %s", network, addr)
			},
			ConnectDone: func(network, addr string, err error) {
				log.Printf("🔌 Connect done: %s %s (err=%v)", network, addr, err)
			},
			GotConn: func(info httptrace.GotConnInfo) {
				log.Printf("✅ GotConn: reused=%v, wasIdle=%v", info.Reused, info.WasIdle)
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, "", nil, err
	}
	defer resp.Body.Close()

	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, resp.Request.URL.String(), resp.Header, readErr
	}
	return resp.StatusCode, b, resp.Request.URL.String(), resp.Header, nil
}

type Personel struct {
	InsanID string `json:"insan_id"`
	TC      string `json:"tc"`
	Ad      string `json:"ad"`
	Soyad   string `json:"soyad"`
	Gorev   string `json:"gorev"`
	Unvan   string `json:"unvan"`
	Sube    string `json:"sube"`
	Telefon string `json:"telefon"`
}

func PersonelListesiHandler(w http.ResponseWriter, r *http.Request) {
	verbose := r.URL.Query().Get("debug") == "1"

	status, body, finalURL, hdr, err := fetchEnibra(verbose)
	if err != nil {
		log.Println("❌ İstek hatası:", err)
		http.Error(w, "Veri alınamadı (istek hatası)", http.StatusBadGateway)
		return
	}

	ct := hdr.Get("Content-Type")
	trimmed := bytes.TrimSpace(body)

	if status < 200 || status >= 300 || len(trimmed) == 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, "Uzak servisten beklenen veri alınamadı.\nstatus=%d %s\nfinalURL=%s\nheaders=%v\n\nBODY (ilk 500):\n%s",
			status, http.StatusText(status), finalURL, hdr, string(trimmed[:min(500, len(trimmed))]))
		return
	}

	if r.URL.Query().Get("raw") == "1" {
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
		return
	}

	list, err := normalizePersonelList(trimmed)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		resp := map[string]any{
			"error":         "invalid_upstream_json",
			"detail":        err.Error(),
			"status":        status,
			"upstream_code": hdr.Get("X-Upstream-Status"),
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	writePersonelListResponse(w, r, list)
}

// JSON endpoint: Upstream JSON'unu sadeleştir, sayıları string'e düzgün çevir, arama+sayfalama uygula
func PersonelDetayHandler(w http.ResponseWriter, r *http.Request) {
	// fetchEnibra(verbose) -> (status int, body []byte, finalURL string, hdr http.Header, err error)
	status, body, _, _, err := fetchEnibra(false)
	if err != nil || status < 200 || status >= 300 || len(bytes.TrimSpace(body)) == 0 {
		http.Error(w, `{"error":"upstream_failed"}`, http.StatusBadGateway)
		return
	}

	// Upstream JSON şeması: { "SONUC_KODU":0, "SONUC_MESAJI":[ {..}, {..} ] }
	var root struct {
		SonucKodu   any              `json:"SONUC_KODU"`
		SonucMesaji []map[string]any `json:"SONUC_MESAJI"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		http.Error(w, `{"error":"invalid_upstream_json"}`, http.StatusBadGateway)
		return
	}

	all := normalizePersonelFromRoot(root.SonucMesaji)

	// Basit arama (q) – ad/soyad/görev/ünvan/şube/tc üzerinde
	writePersonelListResponse(w, r, all)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func normalizePersonelList(body []byte) ([]Personel, error) {
	var root struct {
		SonucMesaji []map[string]any `json:"SONUC_MESAJI"`
	}

	if err := json.Unmarshal(body, &root); err != nil {
		var arr []map[string]any
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
			return nil, errors.New("upstream json beklenen formatta değil")
		}
		root.SonucMesaji = arr
	}

	if len(root.SonucMesaji) == 0 {
		return nil, errors.New("upstream kayit listesi boş")
	}

	return normalizePersonelFromRoot(root.SonucMesaji), nil
}

func normalizePersonelFromRoot(items []map[string]any) []Personel {
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
					return strings.TrimSpace(fmt.Sprint(v))
				}
			}
		}
		return ""
	}

	out := make([]Personel, 0, len(items))
	for _, m := range items {
		out = append(out, Personel{
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
	return out
}

func writePersonelListResponse(w http.ResponseWriter, r *http.Request, all []Personel) {
	q := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
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

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.URL.Query().Get("all") == "1" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": filtered,
			"total": len(filtered),
		})
		return
	}

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

	_ = json.NewEncoder(w).Encode(map[string]any{
		"page":  page,
		"limit": limit,
		"total": total,
		"items": filtered[start:end],
	})
}
