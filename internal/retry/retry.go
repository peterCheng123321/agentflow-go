package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
)

var (
	ErrMaxAttempts = errors.New("max retry attempts exceeded")
)

type Config struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func NewConfig(maxAttempts int, baseDelay time.Duration) Config {
	return Config{
		MaxAttempts: maxAttempts,
		BaseDelay:   baseDelay,
		MaxDelay:    baseDelay * 8,
	}
}

func Do(ctx context.Context, cfg Config, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		if !isRetryable(err) {
			return err
		}

		lastErr = err

		if attempt < cfg.MaxAttempts {
			delay := calcDelay(cfg.BaseDelay, cfg.MaxDelay, attempt-1)
			select {
			case <-ctx.Done():
				return fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
	}

	return fmt.Errorf("%w after %d attempts: %v", ErrMaxAttempts, cfg.MaxAttempts, lastErr)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	for _, pattern := range []string{
		"rate limit",
		"429",
		"too many requests",
		"502",
		"503",
		"504",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"connection refused",
		"connection reset",
		"i/o timeout",
		"timeout",
		"transient",
		"temporary",
		"retry",
	} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}

	return false
}

func calcDelay(base, max time.Duration, attempt int) time.Duration {
	exp := base * time.Duration(1<<uint(attempt))
	if exp > max {
		exp = max
	}
	jitter := time.Duration(rand.Int63n(int64(exp / 2)))
	return exp/2 + jitter
}
