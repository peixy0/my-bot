package inbox

import (
	"context"
	"errors"
)

var (
	ErrFull   = errors.New("inbox full")
	ErrClosed = errors.New("inbox closed")
)

type Inbox[T any] interface {
	Publish(ctx context.Context, msg T) error
	Receive(ctx context.Context) (T, error)
	TryPublish(msg T) bool
	TryReceive() (T, bool)
	Close()
	Len() int
	Cap() int
}

type Memory[T any] struct {
	ch chan T
}

func NewMemory[T any](capacity int) *Memory[T] {
	if capacity < 0 {
		capacity = 0
	}
	return &Memory[T]{ch: make(chan T, capacity)}
}

func (m *Memory[T]) Publish(ctx context.Context, msg T) error {
	select {
	case m.ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Memory[T]) Receive(ctx context.Context) (T, error) {
	var zero T
	select {
	case msg, ok := <-m.ch:
		if !ok {
			return zero, ErrClosed
		}
		return msg, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (m *Memory[T]) TryReceive() (T, bool) {
	var zero T
	select {
	case msg, ok := <-m.ch:
		if !ok {
			return zero, false
		}
		return msg, true
	default:
		return zero, false
	}
}

func (m *Memory[T]) TryPublish(msg T) bool {
	select {
	case m.ch <- msg:
		return true
	default:
		return false
	}
}

func (m *Memory[T]) Close() {
	close(m.ch)
}

func (m *Memory[T]) C() <-chan T { return m.ch }

func (m *Memory[T]) Len() int { return len(m.ch) }

func (m *Memory[T]) Cap() int { return cap(m.ch) }
