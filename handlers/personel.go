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

// DEBUG: upstream’dan geleni olduğu gibi döndür (header’a göre)
func PersonelListesiRawHandler(w http.ResponseWriter, r *http.Request) {
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
// DEBUG: Upstream’dan geleni uygulamanın beklediği JSON’a dönüştür
func PersonelListesiHandler(w http.ResponseWriter, r *http.Request) {
	verbose := r.URL.Query().Get("debug") == "1"

	status, body, finalURL, hdr, err := fetchEnibra(verbose)
	if err != nil {
		log.Println("❌ İstek hatası:", err)
		http.Error(w, `{"error":"upstream_request_failed"}`, http.StatusBadGateway)
		return
	}
	trimmed := bytes.TrimSpace(body)
	if status < 200 || status >= 300 || len(trimmed) == 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"upstream_bad_status","status":%d,"finalURL":%q}`, status, finalURL)
		return
	}

	// Upstream JSON'ı oku (gevşek şema)
	var root map[string]any
	if err := json.Unmarshal(trimmed, &root); err != nil {
		// JSON değilse olduğu gibi pas geç (eski davranış)
		w.Header().Set("Content-Type", hdr.Get("Content-Type"))
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
		return
	}

	// Beklenen uyumlu çıktı: { SONUC_KODU: "0", SONUC_MESAJI: "", DATA: [...] }
	out := map[string]any{
		"SONUC_KODU":   "0",
		"SONUC_MESAJI": "",
		"DATA":         []any{},
	}

	// SONUC_KODU'nu string'e çevir
	if v, ok := root["SONUC_KODU"]; ok && v != nil {
		switch t := v.(type) {
		case string:
			out["SONUC_KODU"] = t
		case float64:
			out["SONUC_KODU"] = fmt.Sprintf("%d", int64(t))
		default:
			out["SONUC_KODU"] = fmt.Sprint(t)
		}
	}

	// Upstream bazen veriyi SONUC_MESAJI altında dizi olarak gönderiyor
	switch v := root["SONUC_MESAJI"].(type) {
	case string:
		out["SONUC_MESAJI"] = v
	case []any:
		out["DATA"] = v
	case []map[string]any:
		arr := make([]any, 0, len(v))
		for _, m := range v {
			arr = append(arr, m)
		}
		out["DATA"] = arr
	default:
		// Bazı sistemler "DATA" anahtarını zaten gönderiyor olabilir
		if data, ok := root["DATA"]; ok {
			out["DATA"] = data
		}
	}

	// İçerik tipini net koy
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(out)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
