// Package embedserver supervises a local Python embedding sidecar
// (scripts/mlx_embed_server.py) that hosts a multilingual MLX embedding
// model. The pattern mirrors internal/mlxserver: SIGINT-first shutdown,
// exponential restart, Ready()/BaseURL()/Status() surface.
//
// Best-effort: if Python or the script is missing, we log it and let the
// rest of the server run. Callers gate on Ready() before sending traffic
// and fall through to an alternate router (or UNKNOWN) on miss.
package embedserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	// Python is the python interpreter (default "python3").
	Python string
	// Script is the absolute path to mlx_embed_server.py. If empty, we look
	// in $AGENTFLOW_EMBED_SERVER_SCRIPT, then ./scripts/mlx_embed_server.py
	// relative to the agentflow-go module root.
	Script string
	// Model is the HF repo id passed via --model.
	Model string
	// Port is the local port the sidecar binds.
	Port int
	// Host is where the sidecar binds; defaults to 127.0.0.1.
	Host string
	// LogPrefix is prepended to forwarded stdout/stderr lines.
	LogPrefix string
}

type Manager struct {
	cfg         Config
	cmdMu       sync.Mutex
	cmd         *exec.Cmd
	cancel      context.CancelFunc
	ready       atomic.Bool
	startedOnce atomic.Bool
}

func New(cfg Config) *Manager {
	if cfg.Python == "" {
		cfg.Python = "python3"
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.LogPrefix == "" {
		cfg.LogPrefix = "[embed]"
	}
	if cfg.Script == "" {
		cfg.Script = os.Getenv("AGENTFLOW_EMBED_SERVER_SCRIPT")
	}
	if cfg.Script == "" {
		// Search candidate locations in order: the running .app bundle's
		// Resources/ dir (production), then ./scripts/ relative to cwd (dev),
		// then $REPO_ROOT/agentflow-go/scripts/ (developer convenience).
		var candidates []string
		if exe, err := os.Executable(); err == nil {
			// /Applications/AgentFlow.app/Contents/MacOS/agentflow-serve
			//   → /Applications/AgentFlow.app/Contents/Resources/mlx_embed_server.py
			candidates = append(candidates,
				filepath.Join(filepath.Dir(exe), "..", "Resources", "mlx_embed_server.py"))
		}
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates,
				filepath.Join(cwd, "scripts", "mlx_embed_server.py"))
			candidates = append(candidates,
				filepath.Join(cwd, "agentflow-go", "scripts", "mlx_embed_server.py"))
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				cfg.Script = p
				break
			}
		}
	}
	return &Manager{cfg: cfg}
}

func (m *Manager) Start(parent context.Context) error {
	if m.cfg.Model == "" {
		return fmt.Errorf("embed model not set")
	}
	if m.cfg.Port <= 0 {
		return fmt.Errorf("embed port not set")
	}
	if m.cfg.Script == "" {
		log.Printf("%s mlx_embed_server.py not found — embed router disabled", m.cfg.LogPrefix)
		return nil
	}
	if _, err := os.Stat(m.cfg.Script); err != nil {
		log.Printf("%s script %q not readable — embed router disabled: %v", m.cfg.LogPrefix, m.cfg.Script, err)
		return nil
	}
	if _, err := exec.LookPath(m.cfg.Python); err != nil {
		log.Printf("%s %s not in PATH — embed router disabled", m.cfg.LogPrefix, m.cfg.Python)
		return nil
	}

	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	go m.supervise(ctx)
	m.startedOnce.Store(true)
	return nil
}

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

func (m *Manager) runOnce(ctx context.Context) error {
	args := []string{
		m.cfg.Script,
		"--model", m.cfg.Model,
		"--host", m.cfg.Host,
		"--port", strconv.Itoa(m.cfg.Port),
	}
	cmd := exec.CommandContext(ctx, m.cfg.Python, args...)
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
		return fmt.Errorf("start: %w", err)
	}
	log.Printf("%s started pid=%d model=%s port=%d", m.cfg.LogPrefix, cmd.Process.Pid, m.cfg.Model, m.cfg.Port)

	m.cmdMu.Lock()
	m.cmd = cmd
	m.cmdMu.Unlock()

	go forwardLog(m.cfg.LogPrefix, stdout)
	go forwardLog(m.cfg.LogPrefix, stderr)

	go m.waitForReady(ctx)

	err = cmd.Wait()
	m.ready.Store(false)
	return err
}

func (m *Manager) waitForReady(ctx context.Context) {
	url := m.BaseURL() + "/health"
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
			log.Printf("%s still warming (%.0fs elapsed)", m.cfg.LogPrefix, time.Since(start).Seconds())
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
	buf := make([]byte, 4096)
	carry := []byte{}
	for {
		n, err := r.Read(buf)
		if n > 0 {
			carry = append(carry, buf[:n]...)
			for {
				idx := indexByte(carry, '\n')
				if idx < 0 {
					break
				}
				line := carry[:idx]
				carry = carry[idx+1:]
				if len(line) > 0 {
					log.Printf("%s %s", prefix, string(line))
				}
			}
		}
		if err != nil {
			if len(carry) > 0 {
				log.Printf("%s %s", prefix, string(carry))
			}
			return
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

func (m *Manager) Ready() bool { return m.ready.Load() }

// BaseURL returns the sidecar's base URL — no /v1 suffix because the
// embed sidecar isn't OpenAI-shaped, it's our own /embed endpoint.
func (m *Manager) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", m.cfg.Host, m.cfg.Port)
}

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
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
}

func (m *Manager) Status() map[string]any {
	return map[string]any{
		"enabled":  m.startedOnce.Load(),
		"ready":    m.ready.Load(),
		"model":    m.cfg.Model,
		"base_url": m.BaseURL(),
	}
}
