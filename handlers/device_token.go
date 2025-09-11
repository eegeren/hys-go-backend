// handlers/device_token.go
package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DeviceToken struct {
	TCKimlikNo string `json:"tc"`
	Platform   string `json:"platform"` // "ios" | "android"
	Token      string `json:"token"`
	UpdatedAt  string `json:"updated_at"`
}

func tokensFile() string {
	_ = os.MkdirAll("data", 0755)
	return filepath.Join("data", "device_tokens.json")
}

func readTokens() ([]DeviceToken, error) {
	b, err := os.ReadFile(tokensFile())
	if err != nil {
		if os.IsNotExist(err) {
			return []DeviceToken{}, nil
		}
		return nil, err
	}
	var list []DeviceToken
	if err := json.Unmarshal(b, &list); err != nil {
		return []DeviceToken{}, nil
	}
	return list, nil
}

func writeTokens(list []DeviceToken) error {
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := tokensFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, tokensFile())
}

// POST /api/device/register
// Body: { "tc":"...", "platform":"android|ios", "token":"FCM_TOKEN" }
func RegisterDeviceToken(w http.ResponseWriter, r *http.Request) {
	var in DeviceToken
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad_request", http.StatusBadRequest)
		return
	}
	in.TCKimlikNo = strings.TrimSpace(in.TCKimlikNo)
	in.Platform = strings.ToLower(strings.TrimSpace(in.Platform))
	in.Token = strings.TrimSpace(in.Token)
	if in.TCKimlikNo == "" || in.Token == "" {
		http.Error(w, "tc_or_token_missing", http.StatusBadRequest)
		return
	}
	in.UpdatedAt = time.Now().Format(time.RFC3339)

	list, err := readTokens()
	if err != nil {
		http.Error(w, "read_error", http.StatusInternalServerError)
		return
	}

	// aynı tc+token varsa güncelle; yoksa ekle
	replaced := false
	for i := range list {
		if list[i].TCKimlikNo == in.TCKimlikNo && list[i].Token == in.Token {
			list[i] = in
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, in)
	}

	if err := writeTokens(list); err != nil {
		http.Error(w, "persist_error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/device/unregister
// Body: { "tc":"...", "token":"FCM_TOKEN" }
func UnregisterDeviceToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TCKimlikNo string `json:"tc"`
		Token      string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad_request", http.StatusBadRequest)
		return
	}
	in.TCKimlikNo = strings.TrimSpace(in.TCKimlikNo)
	in.Token = strings.TrimSpace(in.Token)
	if in.TCKimlikNo == "" || in.Token == "" {
		http.Error(w, "tc_or_token_missing", http.StatusBadRequest)
		return
	}

	list, err := readTokens()
	if err != nil {
		http.Error(w, "read_error", http.StatusInternalServerError)
		return
	}

	out := list[:0]
	for _, t := range list {
		if !(t.TCKimlikNo == in.TCKimlikNo && t.Token == in.Token) {
			out = append(out, t)
		}
	}

	if err := writeTokens(out); err != nil {
		http.Error(w, "persist_error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
