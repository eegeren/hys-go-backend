package main

import (
	"bufio"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/eegeren/hys-go-backend/routes"
	"github.com/gorilla/mux"
)

func init() {
	loadEnvFile(".env")
}

func main() {
	r := mux.NewRouter()
	routes.RegisterRoutes(r)

	r.HandleFunc("/api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","message":"HYS Go Backend Aktif"}`))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}
	addr := "127.0.0.1:" + port

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("🚀 Sunucu çalışıyor: http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe hata: %v", err)
		}
	}()

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Graceful shutdown hata: %v", err)
	} else {
		log.Println("Sunucu düzgün kapatıldı.")
	}
}

func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if eq := strings.Index(line, "="); eq >= 0 {
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			if n := len(val); n >= 2 && ((val[0] == '"' && val[n-1] == '"') || (val[0] == '\'' && val[n-1] == '\'')) {
				val = val[1 : n-1]
			}
			if key != "" && os.Getenv(key) == "" {
				_ = os.Setenv(key, val)
			}
		}
	}
}
