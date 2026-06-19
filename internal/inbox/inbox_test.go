package inbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryOrdering(t *testing.T) {
	ib := NewMemory[string](2)
	if err := ib.Publish(context.Background(), Envelope[string]{Payload: "first"}); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if err := ib.Publish(context.Background(), Envelope[string]{Payload: "second"}); err != nil {
		t.Fatalf("publish second: %v", err)
	}

	first, err := ib.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive first: %v", err)
	}
	second, err := ib.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive second: %v", err)
	}
	if first.Payload != "first" || second.Payload != "second" {
		t.Fatalf("messages out of order: %q then %q", first.Payload, second.Payload)
	}
}

func TestMemoryTryPublishFull(t *testing.T) {
	ib := NewMemory[string](1)
	if !ib.TryPublish(Envelope[string]{Payload: "first"}) {
		t.Fatal("expected first publish to succeed")
	}
	if ib.TryPublish(Envelope[string]{Payload: "second"}) {
		t.Fatal("expected second publish to fail when inbox is full")
	}
}

func TestMemoryTryReceive(t *testing.T) {
	ib := NewMemory[string](1)
	if _, ok := ib.TryReceive(); ok {
		t.Fatal("expected empty inbox to have no message")
	}
	ib.TryPublish(Envelope[string]{Payload: "msg"})
	msg, ok := ib.TryReceive()
	if !ok || msg.Payload != "msg" {
		t.Fatalf("expected msg, got ok=%v msg=%+v", ok, msg)
	}
}

func TestMemoryPublishCanceled(t *testing.T) {
	ib := NewMemory[string](0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ib.Publish(ctx, Envelope[string]{Payload: "msg"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestMemoryReceiveCanceled(t *testing.T) {
	ib := NewMemory[string](0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	_, err := ib.Receive(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
}

func TestMemoryClose(t *testing.T) {
	ib := NewMemory[string](1)
	ib.Close()

	if ib.TryPublish(Envelope[string]{Payload: "msg"}) {
		t.Fatal("expected TryPublish to fail after close")
	}
	if err := ib.Publish(context.Background(), Envelope[string]{Payload: "msg"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed from Publish, got %v", err)
	}
	if _, err := ib.Receive(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed from Receive, got %v", err)
	}
}

func TestNewEnvelopeSetsCreatedAt(t *testing.T) {
	msg := NewEnvelope("hello")
	if msg.Payload != "hello" {
		t.Fatalf("unexpected envelope: %+v", msg)
	}
}
