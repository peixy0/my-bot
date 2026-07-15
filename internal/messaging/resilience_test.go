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
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
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
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("should not be context.DeadlineExceeded")
	}
}
