package inbox

import (
	"context"
	"errors"
	"time"
)

var (
	ErrFull   = errors.New("inbox full")
	ErrClosed = errors.New("inbox closed")
)

type Address struct {
	Kind string
	ID   string
}

type Envelope[T any] struct {
	ID            string
	Kind          string
	Target        Address
	Sender        Address
	CorrelationID string
	ParentID      string
	Payload       T
	CreatedAt     time.Time
}

type Inbox[T any] interface {
	Publish(ctx context.Context, msg Envelope[T]) error
	Receive(ctx context.Context) (Envelope[T], error)
	TryPublish(msg Envelope[T]) bool
	TryReceive() (Envelope[T], bool)
	Close()
	Len() int
	Cap() int
}

type Memory[T any] struct {
	ch chan Envelope[T]
}

func NewMemory[T any](capacity int) *Memory[T] {
	if capacity < 0 {
		capacity = 0
	}
	return &Memory[T]{ch: make(chan Envelope[T], capacity)}
}

func NewEnvelope[T any](kind string, target Address, payload T) Envelope[T] {
	return Envelope[T]{
		Kind:      kind,
		Target:    target,
		Payload:   payload,
		CreatedAt: time.Now(),
	}
}

func (m *Memory[T]) Publish(ctx context.Context, msg Envelope[T]) (err error) {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	defer func() {
		if recover() != nil {
			err = ErrClosed
		}
	}()
	select {
	case m.ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Memory[T]) Receive(ctx context.Context) (Envelope[T], error) {
	select {
	case msg, ok := <-m.ch:
		if !ok {
			return Envelope[T]{}, ErrClosed
		}
		return msg, nil
	case <-ctx.Done():
		return Envelope[T]{}, ctx.Err()
	}
}

func (m *Memory[T]) TryReceive() (Envelope[T], bool) {
	select {
	case msg, ok := <-m.ch:
		if !ok {
			return Envelope[T]{}, false
		}
		return msg, true
	default:
		return Envelope[T]{}, false
	}
}

func (m *Memory[T]) TryPublish(msg Envelope[T]) (ok bool) {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	select {
	case m.ch <- msg:
		return true
	default:
		return false
	}
}

func (m *Memory[T]) Close() {
	defer func() {
		_ = recover()
	}()
	close(m.ch)
}

func (m *Memory[T]) C() <-chan Envelope[T] { return m.ch }

func (m *Memory[T]) Len() int { return len(m.ch) }

func (m *Memory[T]) Cap() int { return cap(m.ch) }
