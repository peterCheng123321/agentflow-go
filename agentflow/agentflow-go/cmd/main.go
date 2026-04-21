package main

import (
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"agentflow-go/internal/config"
	"agentflow-go/internal/core"
	"agentflow-go/internal/ui"
)

func main() {
	// Fyne requires the main goroutine on macOS
	runtime.LockOSThread()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("AgentFlow starting…")

	runtime.GOMAXPROCS(runtime.NumCPU())

	cfg := config.Load()
	log.Printf("Config: llm_backend=%s model=%s", cfg.LLMBackend, cfg.ModelName)

	a := core.New(cfg)

	// Headless mode for testing / CI
	if os.Getenv("AGENTFLOW_NO_UI") != "" {
		log.Println("Headless mode — waiting for signal")
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		a.Shutdown()
		return
	}

	// Graceful shutdown on OS signal (handled inside ShowAndRun via window close)
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		log.Println("Shutting down…")
		a.Shutdown()
		os.Exit(0)
	}()

	ui.Run(a) // blocks until window closed

	log.Println("AgentFlow stopped")
	a.Shutdown()
}
