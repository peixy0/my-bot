package engine

import (
	"context"
	"testing"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
)

type captureOutbound struct {
	messages []string
}

func (o *captureOutbound) Send(_ context.Context, text string) {
	o.messages = append(o.messages, text)
}

func (o *captureOutbound) SendDelta(_ context.Context, text string) {
	o.messages = append(o.messages, text)
}

func (o *captureOutbound) SendFinal(context.Context) {}

func (o *captureOutbound) StartThinking(context.Context) {}
func (o *captureOutbound) EndThinking(context.Context)   {}

func TestSchedulerHeartbeatInterval(t *testing.T) {
	s := &Scheduler{cfg: &config.Config{WakeIntervalSeconds: 1800}}
	out := &captureOutbound{}

	interval, ok := s.heartbeatInterval(context.Background(), nil, out)
	if !ok || interval != 1800 {
		t.Fatalf("default interval mismatch: interval=%d ok=%v", interval, ok)
	}

	interval, ok = s.heartbeatInterval(context.Background(), []string{"60"}, out)
	if !ok || interval != 60 {
		t.Fatalf("override interval mismatch: interval=%d ok=%v", interval, ok)
	}

	_, ok = s.heartbeatInterval(context.Background(), []string{"0"}, out)
	if ok {
		t.Fatal("expected zero interval to be rejected")
	}
	if len(out.messages) != 1 {
		t.Fatalf("expected one validation message, got %d", len(out.messages))
	}
}

func TestSendWorkerEventPublishesEnvelope(t *testing.T) {
	workerInbox := inbox.NewMemory[events.WorkerEvent](1)
	if !sendWorkerEvent("chat-1", workerInbox, events.TextInputEvent{ChatID: "chat-1", Message: "hi"}) {
		t.Fatal("expected worker event publish to succeed")
	}

	msg, err := workerInbox.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive worker event: %v", err)
	}
	if msg.Target.Kind != "worker" || msg.Target.ID != "chat-1" {
		t.Fatalf("unexpected target: %+v", msg.Target)
	}
	if _, ok := msg.Payload.(events.TextInputEvent); !ok {
		t.Fatalf("unexpected payload type: %T", msg.Payload)
	}
}

func TestSendWorkerEventDropsWhenInboxFull(t *testing.T) {
	workerInbox := inbox.NewMemory[events.WorkerEvent](1)
	if !sendWorkerEvent("chat-1", workerInbox, events.TextInputEvent{ChatID: "chat-1"}) {
		t.Fatal("expected first publish to succeed")
	}
	if sendWorkerEvent("chat-1", workerInbox, events.TextInputEvent{ChatID: "chat-1"}) {
		t.Fatal("expected second publish to fail when inbox is full")
	}
}
