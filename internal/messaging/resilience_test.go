package messaging

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCallWithTimeout_Success(t *testing.T) {
	err := CallWithTimeout(context.Background(), time.Second, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCallWithTimeout_TimedOut(t *testing.T) {
	err := CallWithTimeout(context.Background(), 50*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestCallWithTimeout_NonTimeoutError(t *testing.T) {
	myErr := errors.New("boom")
	err := CallWithTimeout(context.Background(), time.Second, func(ctx context.Context) error {
		return myErr
	})
	if !errors.Is(err, myErr) {
		t.Fatalf("expected myErr, got %v", err)
	}
	if errors.Is(err, ErrTimeout) {
		t.Fatal("should not be ErrTimeout")
	}
}

func TestCallWithRetry_SuccessOnFirstTry(t *testing.T) {
	calls := 0
	err := CallWithRetry(context.Background(), func(ctx context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestCallWithRetry_RetriesOnTimeout(t *testing.T) {
	calls := 0
	err := CallWithRetry(context.Background(), func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return ErrTimeout
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestCallWithRetry_NonTimeoutErrorNoRetry(t *testing.T) {
	calls := 0
	myErr := errors.New("non-timeout")
	err := CallWithRetry(context.Background(), func(ctx context.Context) error {
		calls++
		return myErr
	})
	if !errors.Is(err, myErr) {
		t.Fatalf("expected myErr, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestCallWithRetry_AllTimeoutsExhausted(t *testing.T) {
	calls := 0
	err := CallWithRetry(context.Background(), func(ctx context.Context) error {
		calls++
		return ErrTimeout
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestCallWithTimeoutAndRetry_Success(t *testing.T) {
	calls := 0
	err := CallWithTimeoutAndRetry(context.Background(), time.Second, func(ctx context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestCallWithTimeoutAndRetry_RetriesOnTimeout(t *testing.T) {
	calls := 0
	err := CallWithTimeoutAndRetry(context.Background(), time.Second, func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return ErrTimeout
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestCallWithTimeoutAndRetry_NonTimeoutNoRetry(t *testing.T) {
	calls := 0
	myErr := errors.New("boom")
	err := CallWithTimeoutAndRetry(context.Background(), time.Second, func(ctx context.Context) error {
		calls++
		return myErr
	})
	if !errors.Is(err, myErr) {
		t.Fatalf("expected myErr, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}
