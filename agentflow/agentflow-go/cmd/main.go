package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"agentflow-go/internal/server"
	"agentflow-go/internal/config"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("AgentFlow Go starting...")

	// Maximize CPU utilization for Apple Silicon (M1/M2/M3) and batch processing
	// This allows Go to use all available cores for concurrent LLM/OCR operations
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Load configuration
	cfg := config.Load()
	log.Printf("Config: llm_backend=%s model=%s (set AGENTFLOW_LLM_BACKEND / AGENTFLOW_MODEL to override)", cfg.LLMBackend, cfg.ModelName)

	// Create server
	srv := server.New(cfg)

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start server in goroutine
	serverReady := make(chan struct{})
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		log.Printf("HTTP server listening on %s", addr)
		close(serverReady)
		if err := http.ListenAndServe(addr, srv.Router()); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for server to be ready then open browser
	<-serverReady
	if os.Getenv("AGENTFLOW_NO_BROWSER") == "" {
		go func() {
			// Small delay to ensure server is fully ready
			time.Sleep(500 * time.Millisecond)
			url := fmt.Sprintf("http://localhost:%d", cfg.Port)
			log.Printf("Opening browser to %s", url)
			_ = exec.Command("open", url).Run()
		}()
	}

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
