package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/gorilla/mux"
)

type allowItem struct {
	TC   string `json:"tc"`             // 11 haneli
	Role string `json:"role"`           // "admin" veya "patron" vs.
	Name string `json:"name,omitempty"` // opsiyonel (panelde göstermek için)
}

type allowStore struct {
	sync.RWMutex
	ByTC map[string]allowItem
}

var (
	allowDB         = &allowStore{ByTC: map[string]allowItem{}}
	allowFile       = filepath.Join("data", "allowlist.json")
	tcRegex         = regexp.MustCompile(`^\d{11}$`)
	defaultRole     = "admin"
	onceLoadAllowDB sync.Once
)

// --- Public Handlers ---

// GET /api/admin/allowlist
func GetAllowlist(w http.ResponseWriter, r *http.Request) {
	ensureLoaded()

	allowDB.RLock()
	defer allowDB.RUnlock()

	var list []allowItem
	for _, v := range allowDB.ByTC {
		list = append(list, v)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":     len(list),
		"allowlist": list,
	})
}

// POST /api/admin/allowlist   body: {"tc":"25031519376","role":"admin","name":"Yusuf Ege"}
func AddAllowlist(w http.ResponseWriter, r *http.Request) {
	ensureLoaded()

	var in allowItem
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "gecersiz json", http.StatusBadRequest)
		return
	}
	in.TC = normalizeTC(in.TC)
	if in.Role == "" {
		in.Role = defaultRole
	}

	if err := validateTC(in.TC); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	allowDB.Lock()
	allowDB.ByTC[in.TC] = in
	allowDB.Unlock()

	if err := persist(); err != nil {
		log.Println("allowlist kaydedilemedi:", err)
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":   true,
		"item": in,
	})
}

// DELETE /api/admin/allowlist/{tc}
func RemoveAllowlist(w http.ResponseWriter, r *http.Request) {
	ensureLoaded()

	tc := normalizeTC(mux.Vars(r)["tc"])
	if err := validateTC(tc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	allowDB.Lock()
	delete(allowDB.ByTC, tc)
	allowDB.Unlock()

	if err := persist(); err != nil {
		log.Println("allowlist silme kaydi yazilamadi:", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"tc": tc,
	})
}

// --- Helpers ---

func ensureLoaded() {
	onceLoadAllowDB.Do(func() {
		if err := os.MkdirAll(filepath.Dir(allowFile), 0o755); err != nil {
			log.Println("data klasoru olusmadi:", err)
		}
		_ = loadFromDisk()
	})
}

func loadFromDisk() error {
	f, err := os.Open(allowFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // ilk calisma, dosya yok -> bos
		}
		return err
	}
	defer f.Close()

	var list []allowItem
	if err := json.NewDecoder(f).Decode(&list); err != nil {
		return err
	}

	m := make(map[string]allowItem, len(list))
	for _, it := range list {
		m[it.TC] = it
	}

	allowDB.Lock()
	allowDB.ByTC = m
	allowDB.Unlock()
	return nil
}

func persist() error {
	allowDB.RLock()
	defer allowDB.RUnlock()

	var list []allowItem
	for _, v := range allowDB.ByTC {
		list = append(list, v)
	}

	tmp := allowFile + ".tmp"
	if err := writeFileJSON(tmp, list); err != nil {
		return err
	}
	return os.Rename(tmp, allowFile)
}

func writeFileJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func validateTC(tc string) error {
	if !tcRegex.MatchString(tc) {
		return errors.New("tc 11 haneli olmalı")
	}
	return nil
}

func normalizeTC(tc string) string {
	// baştaki/sondaki boşlukları at, yalnızca rakamları bırak
	out := make([]rune, 0, len(tc))
	for _, r := range tc {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}
