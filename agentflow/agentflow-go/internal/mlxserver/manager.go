// Package mlxserver supervises a local mlx_lm.server subprocess that hosts
// the small router model (e.g. mlx-community/Qwen3.5-0.8B-OptiQ-4bit).
//
// The supervisor is best-effort: if mlx_lm.server is missing from PATH, or
// if the model fails to load, we log it and keep going — the rest of the
// server still runs against the cloud synth model. Callers check Ready()
// before routing traffic at the local endpoint.
package mlxserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Config controls the supervisor.
type Config struct {
	// Cmd is the executable to run (default "mlx_lm.server").
	Cmd string
	// Model is the HF repo id passed via --model.
	Model string
	// Port is the local port mlx_lm.server binds (--port).
	Port int
	// Host is where mlx_lm.server binds; defaults to 127.0.0.1.
	Host string
	// LogPrefix is prepended to forwarded stdout/stderr lines.
	LogPrefix string
	// ExtraArgs are passed verbatim after the canonical flags. Use for
	// optional knobs like --draft-model, --num-draft-tokens, etc.
	ExtraArgs []string
}

// Manager owns one mlx_lm.server child process. It restarts the child on
// crash with simple backoff, and exposes Ready() / BaseURL() for callers.
type Manager struct {
	cfg     Config
	cmdMu   sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	ready   atomic.Bool
	startedOnce atomic.Bool
}

// New constructs a Manager. It does not start anything.
func New(cfg Config) *Manager {
	if cfg.Cmd == "" {
		cfg.Cmd = "mlx_lm.server"
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.LogPrefix == "" {
		cfg.LogPrefix = "[router]"
	}
	return &Manager{cfg: cfg}
}

// Start launches the supervisor goroutine. Returns nil on a clean handoff;
// errors are logged and surfaced via Ready()=false.
func (m *Manager) Start(parent context.Context) error {
	if m.cfg.Model == "" {
		return fmt.Errorf("router model not set")
	}
	if m.cfg.Port <= 0 {
		return fmt.Errorf("router port not set")
	}
	if _, err := exec.LookPath(m.cfg.Cmd); err != nil {
		// Not fatal: log and let Ready() stay false.
		log.Printf("%s %s not in PATH — router disabled (install with `pip install mlx-lm`): %v",
			m.cfg.LogPrefix, m.cfg.Cmd, err)
		return nil
	}

	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	go m.supervise(ctx)
	m.startedOnce.Store(true)
	return nil
}

// supervise spawns the child, forwards logs, and restarts on crash with
// exponential backoff capped at 30s. Returns when ctx is cancelled.
func (m *Manager) supervise(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := m.runOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("%s child exited: %v (restart in %s)", m.cfg.LogPrefix, err, backoff)
		}
		m.ready.Store(false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// runOnce starts mlx_lm.server, polls /v1/models until it responds, then
// blocks until the child exits or ctx is cancelled.
func (m *Manager) runOnce(ctx context.Context) error {
	args := []string{
		"--model", m.cfg.Model,
		"--host", m.cfg.Host,
		"--port", strconv.Itoa(m.cfg.Port),
	}
	args = append(args, m.cfg.ExtraArgs...)
	cmd := exec.CommandContext(ctx, m.cfg.Cmd, args...)
	// Send SIGINT (not SIGKILL) when ctx cancels, so the Python server can
	// flush + close its socket cleanly. Hard-kill after 5s if it ignores us.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	cmd.Env = append(os.Environ(),
		"PYTHONUNBUFFERED=1",
		"HF_HUB_DISABLE_TELEMETRY=1",
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", m.cfg.Cmd, err)
	}
	log.Printf("%s started pid=%d model=%s port=%d", m.cfg.LogPrefix, cmd.Process.Pid, m.cfg.Model, m.cfg.Port)

	m.cmdMu.Lock()
	m.cmd = cmd
	m.cmdMu.Unlock()

	go forwardLog(m.cfg.LogPrefix, stdout)
	go forwardLog(m.cfg.LogPrefix, stderr)

	// Poll for readiness in parallel with the child running. If the child
	// dies before /v1/models responds, cmd.Wait() below will pick it up.
	go m.waitForReady(ctx)

	err = cmd.Wait()
	m.ready.Store(false)
	return err
}

// waitForReady polls /v1/models until the server responds, ctx is cancelled,
// or the child exits (caller signals via ctx). No artificial timeout — first
// runs can take many minutes to download model weights, and any deadline
// shorter than that just leaves Ready() permanently false on a healthy child.
//
// Logs a "still warming" hint every 30s so the operator knows it's working.
func (m *Manager) waitForReady(ctx context.Context) {
	url := m.BaseURL() + "/models"
	client := &http.Client{Timeout: 3 * time.Second}
	start := time.Now()
	lastHint := start
	for {
		if ctx.Err() != nil {
			return
		}
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				m.ready.Store(true)
				log.Printf("%s ready at %s (warmup %.1fs)", m.cfg.LogPrefix, m.BaseURL(), time.Since(start).Seconds())
				return
			}
		}
		if time.Since(lastHint) >= 30*time.Second {
			log.Printf("%s still warming up (%.0fs elapsed) — first run downloads model weights", m.cfg.LogPrefix, time.Since(start).Seconds())
			lastHint = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func forwardLog(prefix string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			log.Printf("%s %s", prefix, line)
		}
	}
}

// Ready reports whether the local server is currently serving requests.
func (m *Manager) Ready() bool { return m.ready.Load() }

// BaseURL returns the OpenAI-compatible base URL of the local server.
// Includes the /v1 prefix mlx_lm.server uses.
func (m *Manager) BaseURL() string {
	return fmt.Sprintf("http://%s:%d/v1", m.cfg.Host, m.cfg.Port)
}

// Stop terminates the child process and the supervisor goroutine.
func (m *Manager) Stop() {
	if !m.startedOnce.Load() {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.cmdMu.Lock()
	cmd := m.cmd
	m.cmdMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		// Give it 3s to exit cleanly, then SIGKILL.
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
}

// Status returns a small map suitable for inclusion in /health output.
func (m *Manager) Status() map[string]any {
	return map[string]any{
		"enabled":  m.startedOnce.Load(),
		"ready":    m.ready.Load(),
		"model":    m.cfg.Model,
		"base_url": m.BaseURL(),
	}
}
