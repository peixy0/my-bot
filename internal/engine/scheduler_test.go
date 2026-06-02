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

func TestSchedulerDispatchTextInputPublishesToExistingWorkerInLoopInbox(t *testing.T) {
	worker := &ConversationWorker{
		Events:      inbox.NewMemory[events.WorkerEvent](1),
		InLoopInbox: inbox.NewMemory[events.WorkerEvent](1),
	}
	s := &Scheduler{
		workers: map[string]*workerEntry{
			"chat-1": {worker: worker},
		},
	}

	s.dispatchUserInput(context.Background(), "chat-1", events.TextInputEvent{
		ChatID:    "chat-1",
		MessageID: "msg-1",
		Message:   "change direction",
		Sender:    &captureOutbound{},
	})

	msg, err := worker.InLoopInbox.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive in-loop input: %v", err)
	}
	ev, ok := msg.Payload.(events.TextInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg.Payload)
	}
	if ev.Message != "change direction" {
		t.Fatalf("unexpected in-loop text: %q", ev.Message)
	}
	if worker.Events.Len() != 0 {
		t.Fatalf("expected worker queue to stay empty, got %d", worker.Events.Len())
	}
}

func TestSchedulerDispatchImageInputPublishesToExistingWorkerInLoopInbox(t *testing.T) {
	worker := &ConversationWorker{
		Events:      inbox.NewMemory[events.WorkerEvent](1),
		InLoopInbox: inbox.NewMemory[events.WorkerEvent](1),
	}
	s := &Scheduler{
		workers: map[string]*workerEntry{
			"chat-1": {worker: worker},
		},
	}

	s.dispatchUserInput(context.Background(), "chat-1", events.ImageInputEvent{
		ChatID:    "chat-1",
		MessageID: "msg-1",
		Message:   "look at this",
		MIMEType:  "image/png",
		ImageData: []byte{1, 2, 3},
		Sender:    &captureOutbound{},
	})

	msg, err := worker.InLoopInbox.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive in-loop input: %v", err)
	}
	ev, ok := msg.Payload.(events.ImageInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg.Payload)
	}
	if ev.Message != "look at this" || ev.MIMEType != "image/png" {
		t.Fatalf("unexpected in-loop image input: %+v", ev)
	}
	if worker.Events.Len() != 0 {
		t.Fatalf("expected worker queue to stay empty, got %d", worker.Events.Len())
	}
}

func TestSchedulerDispatchTextInputQueuesWhenInLoopInboxFull(t *testing.T) {
	worker := &ConversationWorker{
		Events:      inbox.NewMemory[events.WorkerEvent](1),
		InLoopInbox: inbox.NewMemory[events.WorkerEvent](1),
	}
	worker.InLoopInbox.TryPublish(workerEnvelope("chat-1", events.TextInputEvent{ChatID: "chat-1", Message: "already pending"}))
	s := &Scheduler{
		workers: map[string]*workerEntry{
			"chat-1": {worker: worker},
		},
	}

	s.dispatchUserInput(context.Background(), "chat-1", events.TextInputEvent{
		ChatID:  "chat-1",
		Message: "fallback task",
		Sender:  &captureOutbound{},
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive worker event: %v", err)
	}
	ev, ok := msg.Payload.(events.TextInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg.Payload)
	}
	if ev.Message != "fallback task" {
		t.Fatalf("unexpected queued text: %q", ev.Message)
	}
}

func TestSchedulerQueueCommandQueuesWithExistingWorker(t *testing.T) {
	worker := &ConversationWorker{
		Events:      inbox.NewMemory[events.WorkerEvent](1),
		InLoopInbox: inbox.NewMemory[events.WorkerEvent](1),
	}
	s := &Scheduler{
		workers: map[string]*workerEntry{
			"chat-1": {worker: worker},
		},
	}

	s.handleSlashCommand(context.Background(), "queue handle this later", events.TextInputEvent{
		ChatID:  "chat-1",
		Message: "/queue handle this later",
		Sender:  &captureOutbound{},
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive worker event: %v", err)
	}
	ev, ok := msg.Payload.(events.TextInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg.Payload)
	}
	if ev.Message != "handle this later" {
		t.Fatalf("unexpected queued text: %q", ev.Message)
	}
	if worker.InLoopInbox.Len() != 0 {
		t.Fatalf("expected in-loop inbox to stay empty, got %d", worker.InLoopInbox.Len())
	}
}

func TestSchedulerRemovedSteerCommandQueuesAsUnknownSlashCommand(t *testing.T) {
	worker := &ConversationWorker{
		Events:      inbox.NewMemory[events.WorkerEvent](1),
		InLoopInbox: inbox.NewMemory[events.WorkerEvent](1),
	}
	s := &Scheduler{
		workers: map[string]*workerEntry{
			"chat-1": {worker: worker},
		},
	}

	s.handleSlashCommand(context.Background(), "steer old command", events.TextInputEvent{
		ChatID:  "chat-1",
		Message: "/steer old command",
		Sender:  &captureOutbound{},
	})

	if worker.InLoopInbox.Len() != 0 {
		t.Fatalf("expected removed /steer command not to publish in-loop input, got %d", worker.InLoopInbox.Len())
	}
	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive worker event: %v", err)
	}
	ev, ok := msg.Payload.(events.TextInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg.Payload)
	}
	if ev.Message != "/steer old command" {
		t.Fatalf("unexpected queued text: %q", ev.Message)
	}
}
