package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"agentflow-go/internal/config"
	"agentflow-go/internal/server"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	runtime.GOMAXPROCS(runtime.NumCPU())

	cfg := config.Load()
	port := cfg.Port
	if port == 0 {
		port = 8080
	}

	log.Printf("AgentFlow API server starting on :%d (backend=%s model=%s)", port, cfg.LLMBackend, cfg.ModelName)

	srv := server.New(cfg)

	httpSrv := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      corsMiddleware(srv.Router()),
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()
	log.Printf("Listening on http://127.0.0.1:%d", port)

	<-quit
	log.Println("Shutting down…")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	srv.Shutdown()
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Local API: permissive for browser devtools and embedded web views.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
