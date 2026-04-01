package retry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDoSuccessFirstAttempt(t *testing.T) {
	callCount := 0
	cfg := NewConfig(3, 10*time.Millisecond)
	err := Do(context.Background(), cfg, func() error {
		callCount++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestDoSuccessAfterRetry(t *testing.T) {
	callCount := 0
	cfg := NewConfig(3, 10*time.Millisecond)
	err := Do(context.Background(), cfg, func() error {
		callCount++
		if callCount < 3 {
			return errors.New("transient error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
}

func TestDoMaxAttemptsExhausted(t *testing.T) {
	callCount := 0
	cfg := NewConfig(3, 10*time.Millisecond)
	err := Do(context.Background(), cfg, func() error {
		callCount++
		return errors.New("transient error")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
	if !errors.Is(err, ErrMaxAttempts) {
		t.Fatalf("expected ErrMaxAttempts, got %v", err)
	}
}

func TestDoNonRetryableError(t *testing.T) {
	callCount := 0
	cfg := NewConfig(3, 10*time.Millisecond)
	nonRetryable := errors.New("validation error: bad input")
	err := Do(context.Background(), cfg, func() error {
		callCount++
		return nonRetryable
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call (no retry), got %d", callCount)
	}
	if !errors.Is(err, nonRetryable) {
		t.Fatalf("expected original error, got %v", err)
	}
}

func TestDoContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0
	cfg := NewConfig(5, 100*time.Millisecond)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Do(ctx, cfg, func() error {
			callCount++
			return errors.New("transient")
		})
	}()
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error from context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Do did not return after context cancellation")
	}
}

func TestDoRateLimitErrorIsRetryable(t *testing.T) {
	callCount := 0
	cfg := NewConfig(2, 10*time.Millisecond)
	err := Do(context.Background(), cfg, func() error {
		callCount++
		return fmt.Errorf("rate limit exceeded: 429")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (rate limit is retryable), got %d", callCount)
	}
}

func TestDoServiceUnavailableErrorIsRetryable(t *testing.T) {
	callCount := 0
	cfg := NewConfig(2, 10*time.Millisecond)
	err := Do(context.Background(), cfg, func() error {
		callCount++
		return fmt.Errorf("503 service unavailable")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (503 is retryable), got %d", callCount)
	}
}

func TestDoConnectionRefusedIsRetryable(t *testing.T) {
	callCount := 0
	cfg := NewConfig(2, 10*time.Millisecond)
	err := Do(context.Background(), cfg, func() error {
		callCount++
		return fmt.Errorf("dial tcp: connection refused")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (connection refused is retryable), got %d", callCount)
	}
}

func TestCalcDelayJitter(t *testing.T) {
	base := 100 * time.Millisecond
	max := 800 * time.Millisecond
	delays := make([]time.Duration, 100)
	for i := 0; i < 100; i++ {
		delays[i] = calcDelay(base, max, 0)
	}
	minDelay := base / 2
	maxDelay := base * 3 / 2
	for _, d := range delays {
		if d < minDelay || d > maxDelay {
			t.Errorf("delay %v outside expected range [%v, %v]", d, minDelay, maxDelay)
		}
	}
}

func TestDoSingleAttempt(t *testing.T) {
	callCount := 0
	cfg := NewConfig(1, 10*time.Millisecond)
	err := Do(context.Background(), cfg, func() error {
		callCount++
		return errors.New("transient")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}
