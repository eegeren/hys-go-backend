package routes

import (
	"encoding/json"
	"net/http"
	"os"

	"hys-go-backend/handlers"

	"github.com/gorilla/mux"
)

func RegisterRoutes(r *mux.Router) {
	// Middlewares (önce CORS, sonra JSON)
	r.Use(corsMiddleware)
	r.Use(defaultJSONMiddleware)

	api := r.PathPrefix("/api").Subrouter()

	// --- Health & Version ---
	api.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"message": "HYS Go Backend Aktif",
		})
	}).Methods(http.MethodGet)

	api.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "hys-go-backend",
			"version": env("APP_VERSION", "dev"),
		})
	}).Methods(http.MethodGet)

	// --- Enibra proxy ---
	api.HandleFunc("/enibra/personeller", handlers.EnibraPersonelListesiProxy).Methods(http.MethodGet)
}

// ------------ helpers ------------
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			// credentials gerekiyorsa origin'i yansıt
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}

		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token, X-Requested-With")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Type")

		// Preflight ise hemen dön
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func defaultJSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// yalnızca JSON yanıtlar için varsayılan content-type
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
