package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
)

type captureOutbound struct {
	messages []string
}

func (o *captureOutbound) Send(_ context.Context, text string) {
	o.messages = append(o.messages, text)
}

func (o *captureOutbound) SendBegin(context.Context) {}

func (o *captureOutbound) SendDelta(_ context.Context, text string) {
	o.messages = append(o.messages, text)
}

func (o *captureOutbound) SendFinal(context.Context) {}

func (o *captureOutbound) StartThinking(context.Context) {}
func (o *captureOutbound) EndThinking(context.Context)   {}

func newConfigTestWorker(cfg *config.Config) *ConversationWorker {
	return newConversationWorker("chat-1", cfg, NewAgent(nil, nil), nil, nil, nil)
}

func newStubSession(worker *ConversationWorker) *chatSession {
	return &chatSession{chatID: "chat-1", worker: worker}
}

func TestSchedulerHeartbeatInterval(t *testing.T) {
	s := &Scheduler{cfg: &config.Config{Heartbeat: config.HeartbeatConfig{IntervalSeconds: 1800}}}
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

func TestSchedulerDispatchTextInputPublishesToExistingWorkerMessageInbox(t *testing.T) {
	worker := &ConversationWorker{
		Events:       inbox.NewMemory[events.WorkerEvent](1),
		MessageInbox: inbox.NewMemory[events.MessageEvent](1),
	}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.dispatchUserInput(context.Background(), "chat-1", events.TextInputEvent{
		ChatID:    "chat-1",
		MessageID: "msg-1",
		Message:   "change direction",
		Sender:    &captureOutbound{},
	})

	msg, err := worker.MessageInbox.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive in-loop input: %v", err)
	}
	ev, ok := msg.(events.TextInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Message != "change direction" {
		t.Fatalf("unexpected in-loop text: %q", ev.Message)
	}
	if worker.Events.Len() != 0 {
		t.Fatalf("expected worker queue to stay empty, got %d", worker.Events.Len())
	}
}

func TestSchedulerDispatchImageInputPublishesToExistingWorkerMessageInbox(t *testing.T) {
	worker := &ConversationWorker{
		Events:       inbox.NewMemory[events.WorkerEvent](1),
		MessageInbox: inbox.NewMemory[events.MessageEvent](1),
	}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
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

	msg, err := worker.MessageInbox.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive in-loop input: %v", err)
	}
	ev, ok := msg.(events.ImageInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Message != "look at this" || ev.MIMEType != "image/png" {
		t.Fatalf("unexpected in-loop image input: %+v", ev)
	}
	if worker.Events.Len() != 0 {
		t.Fatalf("expected worker queue to stay empty, got %d", worker.Events.Len())
	}
}

func TestSchedulerDispatchTextInputQueuesWhenMessageInboxFull(t *testing.T) {
	worker := &ConversationWorker{
		Events:       inbox.NewMemory[events.WorkerEvent](1),
		MessageInbox: inbox.NewMemory[events.MessageEvent](1),
	}
	worker.MessageInbox.TryPublish(events.TextInputEvent{ChatID: "chat-1", Message: "already pending"})
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.dispatchToSession(context.Background(), "chat-1", events.QueuedInputEvent{
		ChatID:  "chat-1",
		Message: "fallback task",
		Sender:  &captureOutbound{},
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive worker event: %v", err)
	}
	ev, ok := msg.(events.QueuedInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Message != "fallback task" {
		t.Fatalf("unexpected queued text: %q", ev.Message)
	}
}

func TestSchedulerQueueCommandQueuesWithExistingWorker(t *testing.T) {
	worker := &ConversationWorker{
		Events:       inbox.NewMemory[events.WorkerEvent](1),
		MessageInbox: inbox.NewMemory[events.MessageEvent](1),
	}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
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
	ev, ok := msg.(events.QueuedInputEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Message != "handle this later" {
		t.Fatalf("unexpected queued text: %q", ev.Message)
	}
	if worker.MessageInbox.Len() != 0 {
		t.Fatalf("expected in-loop inbox to stay empty, got %d", worker.MessageInbox.Len())
	}
}

func TestSchedulerDumpCommandQueuesUUIDDump(t *testing.T) {
	worker := &ConversationWorker{
		Events:       inbox.NewMemory[events.WorkerEvent](1),
		MessageInbox: inbox.NewMemory[events.MessageEvent](1),
	}
	out := &captureOutbound{}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.handleSlashCommand(context.Background(), "dump", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive dump event: %v", err)
	}
	ev, ok := msg.(events.DumpCommand)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if _, err := uuid.Parse(ev.ID); err != nil {
		t.Fatalf("dump id is not a UUID: %q", ev.ID)
	}
	if ev.Sender != out {
		t.Fatal("expected dump command to keep original sender")
	}
	if len(out.messages) != 0 {
		t.Fatalf("expected scheduler not to send dump success before worker writes, got %v", out.messages)
	}
}

func TestSchedulerDumpCommandRequiresActiveSession(t *testing.T) {
	out := &captureOutbound{}
	s := &Scheduler{sessions: map[string]*chatSession{}}

	s.handleSlashCommand(context.Background(), "dump", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	if len(out.messages) != 1 || out.messages[0] != "no active session" {
		t.Fatalf("unexpected response: %v", out.messages)
	}
}

func TestSchedulerResumeCommandQueuesResume(t *testing.T) {
	worker := &ConversationWorker{
		Events:       inbox.NewMemory[events.WorkerEvent](1),
		MessageInbox: inbox.NewMemory[events.MessageEvent](1),
	}
	out := &captureOutbound{}
	id := uuid.NewString()
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.handleSlashCommand(context.Background(), "resume "+id, events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive resume event: %v", err)
	}
	ev, ok := msg.(events.ResumeCommand)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.ID != id || ev.Sender != out {
		t.Fatalf("unexpected resume event: %+v", ev)
	}
}

func TestSchedulerResumeCommandValidatesUsageAndUUID(t *testing.T) {
	out := &captureOutbound{}
	s := &Scheduler{sessions: map[string]*chatSession{}}

	s.handleSlashCommand(context.Background(), "resume", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})
	s.handleSlashCommand(context.Background(), "resume not-a-uuid", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	want := []string{"usage: /resume <id>", "resume id must be a UUID"}
	if len(out.messages) != len(want) {
		t.Fatalf("unexpected responses: %v", out.messages)
	}
	for i := range want {
		if out.messages[i] != want[i] {
			t.Fatalf("response %d = %q, want %q", i, out.messages[i], want[i])
		}
	}
}

func TestSchedulerModelCommandQueuesConfigChange(t *testing.T) {
	worker := &ConversationWorker{
		Events:       inbox.NewMemory[events.WorkerEvent](1),
		MessageInbox: inbox.NewMemory[events.MessageEvent](1),
	}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.handleSlashCommand(context.Background(), "model gpt-test", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: &captureOutbound{},
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive config event: %v", err)
	}
	ev, ok := msg.(events.ConfigChangeEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Key != events.ConfigKeyModel || ev.Value != "gpt-test" {
		t.Fatalf("unexpected model event: %+v", ev)
	}
}

func TestWorkerResumeLoadsConversation(t *testing.T) {
	sessionDir := t.TempDir()
	worker := newConfigTestWorker(&config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	})
	out := &captureOutbound{}
	id := uuid.NewString()
	messages := []llm.ChatMessage{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer"},
	}
	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, id+".json"), data, 0600); err != nil {
		t.Fatalf("write conversation: %v", err)
	}

	if err := worker.processResume(context.Background(), events.ResumeCommand{ID: id, Sender: out}); err != nil {
		t.Fatalf("resume conversation: %v", err)
	}
	if len(out.messages) != 1 || out.messages[0] != "session resumed: "+id {
		t.Fatalf("unexpected response: %v", out.messages)
	}

	dumpPath := filepath.Join(sessionDir, "after.json")
	if err := worker.loop.DumpConversation(dumpPath); err != nil {
		t.Fatalf("dump resumed conversation: %v", err)
	}
	var got []llm.ChatMessage
	dumped, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dumped conversation: %v", err)
	}
	if err := json.Unmarshal(dumped, &got); err != nil {
		t.Fatalf("unmarshal dumped conversation: %v", err)
	}
	if len(got) != 2 || got[0].Content != "old question" || got[1].Content != "old answer" {
		t.Fatalf("unexpected resumed messages: %#v", got)
	}
}

func TestWorkerDumpWritesUUIDConversation(t *testing.T) {
	sessionDir := t.TempDir()
	worker := newConfigTestWorker(&config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	})
	out := &captureOutbound{}
	id := uuid.NewString()

	if err := worker.processDump(context.Background(), events.DumpCommand{ID: id, Sender: out}); err != nil {
		t.Fatalf("dump conversation: %v", err)
	}
	if len(out.messages) != 1 || out.messages[0] != "session dumped, load with: /resume "+id {
		t.Fatalf("unexpected response: %v", out.messages)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, id+".json")); err != nil {
		t.Fatalf("expected dumped file: %v", err)
	}
}

func TestSchedulerVisionCommandReportsCurrentSetting(t *testing.T) {
	worker := newConfigTestWorker(&config.Config{LLM: config.LLMConfig{Vision: true}, Tool: config.ToolConfig{MaxOutputChars: 1000}})
	out := &captureOutbound{}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.handleSlashCommand(context.Background(), "vision", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive config event: %v", err)
	}
	ev, ok := msg.(events.ConfigQueryEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Key != events.ConfigKeyVision {
		t.Fatalf("unexpected query key: %q", ev.Key)
	}
	if err := worker.processConfigQuery(context.Background(), ev); err != nil {
		t.Fatalf("process config query: %v", err)
	}
	if len(out.messages) != 1 || out.messages[0] != "current vision: on" {
		t.Fatalf("unexpected response: %v", out.messages)
	}
}

func TestSchedulerVisionCommandTogglesWorkerSetting(t *testing.T) {
	worker := newConfigTestWorker(&config.Config{Tool: config.ToolConfig{MaxOutputChars: 1000}})
	out := &captureOutbound{}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.handleSlashCommand(context.Background(), "vision on", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})
	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive config event: %v", err)
	}
	ev, ok := msg.(events.ConfigChangeEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Key != events.ConfigKeyVision || ev.Value != "on" {
		t.Fatalf("unexpected vision event: %+v", ev)
	}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("process config change: %v", err)
	}
	if !worker.VisionSupported() {
		t.Fatal("expected vision to be enabled")
	}

	s.handleSlashCommand(context.Background(), "vision off", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})
	msg, err = worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive config event: %v", err)
	}
	ev, ok = msg.(events.ConfigChangeEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Key != events.ConfigKeyVision || ev.Value != "off" {
		t.Fatalf("unexpected vision event: %+v", ev)
	}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("process config change: %v", err)
	}
	if worker.VisionSupported() {
		t.Fatal("expected vision to be disabled")
	}
	if len(out.messages) != 2 || out.messages[0] != "vision set to: on" || out.messages[1] != "vision set to: off" {
		t.Fatalf("unexpected responses: %v", out.messages)
	}
}

func TestWorkerVisionConfigChangeRejectsInvalidValue(t *testing.T) {
	worker := newConfigTestWorker(&config.Config{Tool: config.ToolConfig{MaxOutputChars: 1000}})
	out := &captureOutbound{}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	s.handleSlashCommand(context.Background(), "vision maybe", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive config event: %v", err)
	}
	ev, ok := msg.(events.ConfigChangeEvent)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("process config change: %v", err)
	}
	if worker.VisionSupported() {
		t.Fatal("expected invalid value to leave vision disabled")
	}
	if len(out.messages) != 1 || out.messages[0] != "usage: /vision on|off" {
		t.Fatalf("unexpected response: %v", out.messages)
	}
}

func TestWorkerGenerationConfigCommands(t *testing.T) {
	worker := newConfigTestWorker(&config.Config{LLM: config.LLMConfig{Temperature: 1, TopP: 1, ContextWindow: 32000}, Tool: config.ToolConfig{MaxOutputChars: 1000}, Context: config.ContextConfig{MaxOutputTokens: 16384}})
	out := &captureOutbound{}
	s := &Scheduler{
		sessions: map[string]*chatSession{
			"chat-1": newStubSession(worker),
		},
	}

	for _, cmd := range []string{"temperature 0.2", "top_p 0.9", "top_k 40", "max_tokens 12000", "context_window 64000"} {
		s.handleSlashCommand(context.Background(), cmd, events.TextInputEvent{
			ChatID: "chat-1",
			Sender: out,
		})
		msg, err := worker.Events.Receive(context.Background())
		if err != nil {
			t.Fatalf("receive config event: %v", err)
		}
		ev, ok := msg.(events.ConfigChangeEvent)
		if !ok {
			t.Fatalf("unexpected payload type: %T", msg)
		}
		if err := worker.processConfigChange(context.Background(), ev); err != nil {
			t.Fatalf("process config change: %v", err)
		}
	}

	if worker.cfg.LLM.Temperature != 0.2 || worker.cfg.LLM.TopP != 0.9 || worker.cfg.LLM.TopK != 40 || worker.cfg.Context.MaxOutputTokens != 12000 || worker.cfg.LLM.ContextWindow != 64000 {
		t.Fatalf("unexpected generation config: %+v", worker.cfg)
	}
	want := []string{"temperature set to: 0.2", "top_p set to: 0.9", "top_k set to: 40", "max_tokens set to: 12000", "context_window set to: 64000"}
	if len(out.messages) != len(want) {
		t.Fatalf("unexpected responses: %v", out.messages)
	}
	for i := range want {
		if out.messages[i] != want[i] {
			t.Fatalf("response %d = %q, want %q", i, out.messages[i], want[i])
		}
	}
}
