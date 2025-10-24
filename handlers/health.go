package handlers

import "net/http"

// Health returns a simple ok response for readiness checks.
func Health(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
