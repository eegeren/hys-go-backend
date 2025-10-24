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

	"hys-go-backend/routes"
)

func main() {
	loadEnvFile(".env")
	initTimeZone()

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "9090"
	}
	addr := "127.0.0.1:" + port

	router := routes.NewRouter()

	server := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  20 * time.Second,
		WriteTimeout: 20 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("[INFO] server listening on http://%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[ERROR] listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	<-stop
	log.Printf("[INFO] shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("[ERROR] graceful shutdown failed: %v", err)
	} else {
		log.Printf("[INFO] server stopped gracefully")
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
		if idx := strings.Index(line, "="); idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if key == "" {
				continue
			}
			if strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") && len(val) >= 2 {
				val = strings.Trim(val, "\"")
			}
			if strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") && len(val) >= 2 {
				val = strings.Trim(val, "'")
			}
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, val)
			}
		}
	}
}

func initTimeZone() {
	tz := strings.TrimSpace(os.Getenv("TZ"))
	if tz == "" {
		return
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("[WARN] unable to load TZ=%s: %v", tz, err)
		return
	}
	time.Local = loc
	log.Printf("[INFO] timezone set to %s", tz)
}
