package routes

import (
	"encoding/json"
	"net/http"
	"os"

	"hys-go-backend/handlers"

	"github.com/gorilla/mux"
)

func RegisterRoutes(r *mux.Router) {
	// Global middlewares
	r.Use(corsMiddleware)
	r.Use(defaultJSONMiddleware)

	api := r.PathPrefix("/api").Subrouter()

	// --- Public ---
	api.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "HYS Go Backend Aktif"})
	}).Methods(http.MethodGet)

	api.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"service": "hys-go-backend", "version": env("APP_VERSION", "dev")})
	}).Methods(http.MethodGet)

	// --- Protected: /api/enibra/*
	enibra := api.PathPrefix("/enibra").Subrouter()
	enibra.Use(apiKeyMiddleware)
	enibra.HandleFunc("/personeller", handlers.EnibraPersonelListesiProxy).Methods(http.MethodGet)
}

// ------------ middlewares ------------
func apiKeyMiddleware(next http.Handler) http.Handler {
	required := env("ADMIN_TOKEN", "")
	allowQuery := env("ALLOW_QUERY_KEY", "0") == "1"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if required == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "server_not_configured"})
			return
		}

		key := r.Header.Get("X-API-Key")
		if key == "" && allowQuery {
			// URL paramı ile de kabul et (geçici kolaylık)
			key = r.URL.Query().Get("key")
			if key == "" {
				key = r.URL.Query().Get("x_api_key")
			}
		}

		if key != required {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-API-Key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func defaultJSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		next.ServeHTTP(w, r)
	})
}

// ------------ helpers ------------
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
