package handlers

import (
	"encoding/json"
	"net/http"
)

func GirisHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TCKimlikNo string `json:"tc_kimlik_no"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.TCKimlikNo == "" {
		http.Error(w, "Geçersiz veri", http.StatusBadRequest)
		return
	}

	resp := map[string]any{
		"tc":       input.TCKimlikNo,
		"ad":       "Yusuf",
		"soyad":    "Eren",
		"gorev":    "Bilgi İşlem",
		"sube":     "Merkez",
		"insan_id": 1,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
