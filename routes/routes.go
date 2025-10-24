package routes

import (
	"log"
	"net/http"
	"strings"
	"time"

	"hys-go-backend/handlers"

	"github.com/gorilla/mux"
)

// NewRouter wires all application routes and middlewares.
func NewRouter() *mux.Router {
	r := mux.NewRouter()
	r.Use(corsMiddleware)
	r.Use(loggingMiddleware)

	api := r.PathPrefix("/api").Subrouter()
	api.HandleFunc("/personel", handlers.PersonelList).Methods(http.MethodGet)

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": "not_found",
			"path":  r.URL.Path,
		})
	})

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Requested-With")

		if strings.EqualFold(r.Method, http.MethodOptions) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := lrw.ResponseWriter.Write(b)
	lrw.size += n
	return n, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lrw, r)
		duration := time.Since(start)
		log.Printf("[INFO] %s %s %d %s", r.Method, r.URL.Path, lrw.status, duration.Round(time.Millisecond))
	})
}
