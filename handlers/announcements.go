package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Announcement struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
}

var (
	annFile = filepath.Join("data", "announcements.json")
	annMu   sync.Mutex
)

func ListAnnouncements(w http.ResponseWriter, r *http.Request) {
	annMu.Lock()
	defer annMu.Unlock()

	ensureAnnFile()

	f, err := os.Open(annFile)
	if err != nil {
		http.Error(w, "cannot open announcements", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	var items []Announcement
	_ = json.NewDecoder(f).Decode(&items)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

// POST /api/announcements -> SADECE Patron & IK
func CreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	annMu.Lock()
	defer annMu.Unlock()

	ensureAnnFile()

	var payload struct {
		Title     string `json:"title"`
		Body      string `json:"body"`
		CreatedBy string `json:"created_by"` // opsiyonel: iOS tarafı gönderebilir
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Var olanları oku
	f, err := os.Open(annFile)
	if err != nil {
		http.Error(w, "cannot open announcements", http.StatusInternalServerError)
		return
	}
	var items []Announcement
	_ = json.NewDecoder(f).Decode(&items)
	_ = f.Close()

	ann := Announcement{
		ID:        time.Now().UTC().Format("20060102150405.000"),
		Title:     payload.Title,
		Body:      payload.Body,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		CreatedBy: payload.CreatedBy,
	}
	items = append([]Announcement{ann}, items...) // en üstte görünsün

	// Diske yaz
	tmp := annFile + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		http.Error(w, "cannot persist", http.StatusInternalServerError)
		return
	}
	if err := json.NewEncoder(out).Encode(items); err != nil {
		out.Close()
		http.Error(w, "cannot persist", http.StatusInternalServerError)
		return
	}
	out.Close()
	_ = os.Rename(tmp, annFile)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ann)
}

func ensureAnnFile() {
	_ = os.MkdirAll(filepath.Dir(annFile), 0755)
	if _, err := os.Stat(annFile); os.IsNotExist(err) {
		_ = os.WriteFile(annFile, []byte("[]"), 0644)
	}
}
