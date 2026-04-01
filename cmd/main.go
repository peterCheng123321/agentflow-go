package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agentflow-go/internal/server"
	"agentflow-go/internal/config"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("AgentFlow Go starting...")

	// Load configuration
	cfg := config.Load()
	log.Printf("Config: llm_backend=%s model=%s (set AGENTFLOW_LLM_BACKEND / AGENTFLOW_MODEL to override)", cfg.LLMBackend, cfg.ModelName)

	// Create server
	srv := server.New(cfg)

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start server in goroutine
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		log.Printf("HTTP server listening on %s", addr)
		if err := http.ListenAndServe(addr, srv.Router()); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	log.Println("Shutting down...")

	// Cleanup
	srv.Shutdown()

	// Give 5 seconds for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	_ = shutdownCtx // Use the context
	
	log.Println("AgentFlow Go stopped")
}
