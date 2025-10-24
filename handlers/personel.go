package handlers

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var httpClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	},
}

// PersonelList fetches the Enibra JSON and returns it as-is.
func PersonelList(w http.ResponseWriter, r *http.Request) {
	body, err := fetchPersonelData(r.Context())
	if err != nil {
		status, payload := classifyFetchError(err)
		WriteJSON(w, status, payload)
		return
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "invalid_enibra_json",
			"message": err.Error(),
		})
		return
	}

	WriteJSONValue(w, http.StatusOK, parsed)
}

func fetchPersonelData(ctx context.Context) ([]byte, error) {
	base := strings.TrimSpace(os.Getenv("ENIBRA_URL"))
	if base == "" {
		return nil, fmt.Errorf("%w: ENIBRA_URL missing", errConfig)
	}

	key, err := resolveEnibraKey()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errConfig, err)
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errConfig, err)
	}

	q := parsed.Query()
	if q.Get("key") == "" {
		q.Set("key", key)
	}
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hys-go-backend/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(&io.LimitedReader{R: resp.Body, N: 4096})
		return nil, fmt.Errorf("%w: status=%d body=%s", errUpstreamStatus, resp.StatusCode, string(snippet))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsedJSON any
	if err := json.Unmarshal(body, &parsedJSON); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidJSON, err)
	}

	return body, nil
}

var (
	errConfig         = errors.New("configuration_error")
	errUpstreamStatus = errors.New("upstream_status_error")
	errInvalidJSON    = errors.New("invalid_json")
	errMissingKey     = errors.New("missing_enibra_key")
	errDecodeKey      = errors.New("decode_enibra_key_failed")
)

func classifyFetchError(err error) (int, map[string]any) {
	switch {
	case errors.Is(err, errConfig):
		return http.StatusServiceUnavailable, map[string]any{"error": "configuration_error", "message": err.Error()}
	case errors.Is(err, errInvalidJSON):
		return http.StatusBadGateway, map[string]any{"error": "invalid_enibra_json", "message": err.Error()}
	case errors.Is(err, errUpstreamStatus):
		return http.StatusBadGateway, map[string]any{"error": "enibra_status_error", "message": err.Error()}
	default:
		return http.StatusBadGateway, map[string]any{"error": "enibra_request_failed", "message": err.Error()}
	}
}

// WriteJSON writes map or struct as JSON with indentation.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	WriteJSONValue(w, status, payload)
}

// WriteJSONValue writes any JSON payload using indentation and utf-8.
func WriteJSONValue(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	w.WriteHeader(status)
	_ = enc.Encode(payload)
}

func resolveEnibraKey() (string, error) {
	if key := strings.TrimSpace(os.Getenv("ENIBRA_KEY")); key != "" {
		return key, nil
	}

	encoded := strings.TrimSpace(os.Getenv("ENIBRA_KEY_ENC"))
	if encoded == "" {
		return "", errMissingKey
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDecodeKey, err)
	}
	return string(decoded), nil
}
