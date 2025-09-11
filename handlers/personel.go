package handlers

import (
	"bytes"
	"encoding/json"
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

const enibraURL = "http://ik.hysavm.com.tr:8088/PersonelGuncellemeListesi.doms?MUSTERI_KODU=HYS&PAROLA=mxOTDjCAQvjMbdV"

// Upstream isteğini ayrıntılı yapan yardımcı
func fetchEnibra(verbose bool) (status int, body []byte, finalURL string, hdr http.Header, err error) {
	transport := &http.Transport{
		Proxy:              http.ProxyFromEnvironment,
		DisableCompression: true, // ham body istiyoruz
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

// DEBUG: upstream’dan geleni olduğu gibi döndür (header’a göre)
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

	// Upstream ne diyorsa onu verelim
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	w.Write(body)
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

	// Çıkış şeması (temiz)
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

	// Yardımcı: farklı anahtar varyantlarını dene ve stringe düzgün çevir
	getStr := func(m map[string]any, keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				switch t := v.(type) {
				case string:
					return strings.TrimSpace(t)
				case float64:
					// Sayısal gelen (TC, INSAN_ID vb.) bilimsel gösterimsiz string
					// Tam sayı gibi görünüyorsa ondalıksız yaz
					if t == float64(int64(t)) {
						return strconv.FormatInt(int64(t), 10)
					}
					return strconv.FormatFloat(t, 'f', -1, 64) // exponent yok
				default:
					return strings.TrimSpace(fmt.Sprint(v))
				}
			}
		}
		return ""
	}

	// Tüm kayıtları sadeleştir
	all := make([]Personel, 0, len(root.SonucMesaji))
	for _, m := range root.SonucMesaji {
		all = append(all, Personel{
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

	// Basit arama (q) – ad/soyad/görev/ünvan/şube/tc üzerinde
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

	// all=1 -> tüm kayıtları dön
	if r.URL.Query().Get("all") == "1" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{
			"items": filtered,
			"total": len(filtered),
		})
		return
	}

	// Sayfalama (varsayılanları artırdım)
	page := 1
	limit := 200 // önce 50 idi; daha fazla göster
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

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{
		"page":  page,
		"limit": limit,
		"total": total,
		"items": pageItems,
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
