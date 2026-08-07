package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"my-bot/internal/browser"
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

func (o *captureOutbound) SendFull(_ context.Context, text string, _ *events.ResponseMetadata) {
	o.messages = append(o.messages, text)
}

func (o *captureOutbound) SendBegin(context.Context) {}

func (o *captureOutbound) SendDelta(_ context.Context, text string) {
	o.messages = append(o.messages, text)
}

func (o *captureOutbound) SendFinal(context.Context, *events.ResponseMetadata) {}

func (o *captureOutbound) StartThinking(context.Context) {}
func (o *captureOutbound) EndThinking(context.Context)   {}

func newConfigTestWorker(cfg *config.Config) *ConversationWorker {
	return newConversationWorker("chat-1", SessionEnv{Cfg: cfg, Agent: NewAgent(nil, nil)}, nil)
}

func newStubSession(worker *ConversationWorker) *chatSession {
	if worker.Control == nil {
		worker.Control = inbox.NewMemory[events.WorkerEvent](1)
	}
	return &chatSession{chatID: "chat-1", worker: worker}
}

func TestSchedulerHeartbeatInterval(t *testing.T) {
	s := &Scheduler{env: SessionEnv{Cfg: &config.Config{Heartbeat: config.HeartbeatConfig{IntervalSeconds: 1800}}}}
	out := &captureOutbound{}

	interval, ok := s.handleHeartbeatInterval(context.Background(), nil, out)
	if !ok || interval != 1800 {
		t.Fatalf("default interval mismatch: interval=%d ok=%v", interval, ok)
	}

	interval, ok = s.handleHeartbeatInterval(context.Background(), []string{"60"}, out)
	if !ok || interval != 60 {
		t.Fatalf("override interval mismatch: interval=%d ok=%v", interval, ok)
	}

	_, ok = s.handleHeartbeatInterval(context.Background(), []string{"0"}, out)
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
		ImageData: []events.ImageData{{Data: []byte{1, 2, 3}, MIMEType: "image/png"}},
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
	if ev.Message != "look at this" || len(ev.ImageData) != 1 || ev.ImageData[0].MIMEType != "image/png" {
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

	msg, err := worker.Control.Receive(context.Background())
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
	ev.Result <- nil
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

func TestSchedulerCompressCommandQueuesCompression(t *testing.T) {
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

	s.handleSlashCommand(context.Background(), "compress", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	msg, err := worker.Events.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive compress event: %v", err)
	}
	ev, ok := msg.(events.CompressCommand)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.Sender != out {
		t.Fatal("expected compress command to keep original sender")
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

	msg, err := worker.Control.Receive(context.Background())
	if err != nil {
		t.Fatalf("receive resume event: %v", err)
	}
	ev, ok := msg.(events.ResumeCommand)
	if !ok {
		t.Fatalf("unexpected payload type: %T", msg)
	}
	if ev.ID != id {
		t.Fatalf("unexpected resume event: %+v", ev)
	}
	ev.Result <- nil
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

	msg, err := worker.Control.Receive(context.Background())
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

func TestSchedulerModelsCommandListsRemoteModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"}]}`))
	}))
	defer server.Close()

	out := &captureOutbound{}
	s := &Scheduler{
		env:      SessionEnv{Agent: NewAgent(llm.NewOpenAIProvider(server.URL, "", server.Client()), nil)},
		sessions: map[string]*chatSession{},
	}

	s.handleSlashCommand(context.Background(), "models", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	if len(out.messages) != 1 || out.messages[0] != "available models:\n- a-model\n- z-model" {
		t.Fatalf("unexpected response: %v", out.messages)
	}
	if len(s.sessions) != 0 {
		t.Fatalf("expected /models not to create a session, got %d", len(s.sessions))
	}
}

func TestSchedulerModelsCommandReportsProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	out := &captureOutbound{}
	s := &Scheduler{
		env:      SessionEnv{Agent: NewAgent(llm.NewOpenAIProvider(server.URL, "", server.Client()), nil)},
		sessions: map[string]*chatSession{},
	}

	s.handleSlashCommand(context.Background(), "models", events.TextInputEvent{
		ChatID: "chat-1",
		Sender: out,
	})

	if len(out.messages) != 1 || out.messages[0] != "error listing models: api error (status 503): unavailable\n" {
		t.Fatalf("unexpected response: %v", out.messages)
	}
	if len(s.sessions) != 0 {
		t.Fatalf("expected /models not to create a session, got %d", len(s.sessions))
	}
}

func TestSchedulerRunReturnsRebootAfterWritingRestoreMarker(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	out := &captureOutbound{}
	in := inbox.NewMemory[events.AgentEvent](1)
	s := NewScheduler(
		SessionEnv{
			Cfg: &config.Config{
				Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
				Tool:      config.ToolConfig{MaxOutputChars: 1000},
			},
			Rt:            nil,
			Agent:         NewAgent(nil, nil),
			Skills:        nil,
			BrowserBroker: browser.NewNoopBroker(),
		},
		in,
		nil,
	)
	session := s.getOrCreateSession(ctx, "chat-1")
	session.worker.loop.conv = &llm.Conversation{
		Messages:    []llm.ChatMessage{{Role: "user", Content: "hello"}},
		TotalTokens: 42,
	}
	if err := in.Publish(context.Background(), events.TextInputEvent{
		ChatID:  "chat-1",
		Message: "/reboot",
		Sender:  out,
	}); err != nil {
		t.Fatalf("publish reboot: %v", err)
	}

	err := s.Run(context.Background())
	if !errors.Is(err, ErrReboot) {
		t.Fatalf("expected reboot error, got %v", err)
	}
	if len(out.messages) != 1 || out.messages[0] != "dumped 1 session(s); rebooting" {
		t.Fatalf("unexpected response: %v", out.messages)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, ".checkpoint")); err != nil {
		t.Fatalf("expected restore marker: %v", err)
	}
}

func TestSchedulerRebootContinuesAfterPartialDumpFailure(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	}
	s := NewScheduler(
		SessionEnv{Cfg: cfg, Agent: NewAgent(nil, nil), BrowserBroker: browser.NewNoopBroker()},
		inbox.NewMemory[events.AgentEvent](1),
		nil,
	)
	good := s.getOrCreateSession(ctx, "good")
	good.worker.loop.conv = &llm.Conversation{Messages: []llm.ChatMessage{{Role: "user", Content: "keep"}}}
	bad := s.getOrCreateSession(ctx, "bad")
	bad.worker.loop.conv = &llm.Conversation{Messages: []llm.ChatMessage{{Role: "user", Content: make(chan int)}}}
	out := &captureOutbound{}

	err := s.handleRebootCommand(ctx, events.TextInputEvent{Sender: out})
	if !errors.Is(err, ErrReboot) {
		t.Fatalf("expected reboot despite dump failure, got %v", err)
	}
	if len(out.messages) != 1 || out.messages[0] != "dumped 1/2 session(s); rebooting with 1 error(s)" {
		t.Fatalf("unexpected reboot response: %v", out.messages)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, ".checkpoint")); err != nil {
		t.Fatalf("expected checkpoint for successful session: %v", err)
	}
	s.closeAllSessions()
}

func TestSchedulerRebootContinuesAfterCheckpointFailure(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(sessionDir, ".checkpoint"), 0700); err != nil {
		t.Fatalf("create checkpoint blocker: %v", err)
	}
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	}
	s := NewScheduler(
		SessionEnv{Cfg: cfg, Agent: NewAgent(nil, nil), BrowserBroker: browser.NewNoopBroker()},
		inbox.NewMemory[events.AgentEvent](1),
		nil,
	)
	session := s.getOrCreateSession(ctx, "chat-1")
	session.worker.loop.conv = &llm.Conversation{Messages: []llm.ChatMessage{{Role: "user", Content: "keep"}}}
	out := &captureOutbound{}

	err := s.handleRebootCommand(ctx, events.TextInputEvent{Sender: out})
	if !errors.Is(err, ErrReboot) {
		t.Fatalf("expected reboot despite checkpoint failure, got %v", err)
	}
	if len(out.messages) != 1 || out.messages[0] != "dumped 1/1 session(s); rebooting with 1 error(s)" {
		t.Fatalf("unexpected reboot response: %v", out.messages)
	}
	s.closeAllSessions()
}

func TestSchedulerRestoreSessionsRestoresAndDeletesMarker(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	}
	s := NewScheduler(SessionEnv{Cfg: cfg, Rt: nil, Agent: NewAgent(nil, nil), Skills: nil, BrowserBroker: browser.NewNoopBroker()}, inbox.NewMemory[events.AgentEvent](1), nil)
	session := s.getOrCreateSession(ctx, "chat-1")
	session.worker.loop.conv = &llm.Conversation{
		Messages:    []llm.ChatMessage{{Role: "user", Content: "restore me"}},
		TotalTokens: 42,
	}
	out := &captureOutbound{}
	if err := s.handleRebootCommand(ctx, events.TextInputEvent{Sender: out}); !errors.Is(err, ErrReboot) {
		t.Fatalf("expected reboot preparation to succeed, got %v: %v", err, out.messages)
	}
	s.closeAllSessions()

	restored := NewScheduler(SessionEnv{Cfg: cfg, Rt: nil, Agent: NewAgent(nil, nil), Skills: nil, BrowserBroker: browser.NewNoopBroker()}, inbox.NewMemory[events.AgentEvent](1), nil)
	restored.maybeRestoreSessions(ctx)
	defer restored.closeAllSessions()

	got := restored.sessions["chat-1"].worker.loop.conv
	if len(got.Messages) != 1 || got.Messages[0].Content != "restore me" || got.TotalTokens != 42 {
		t.Fatalf("unexpected restored conversation: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, ".checkpoint")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected restore marker removal, got %v", err)
	}
}

func TestSchedulerRunKeepsRestoreMarkerAfterRestoreFailure(t *testing.T) {
	sessionDir := t.TempDir()
	markerPath := filepath.Join(sessionDir, ".checkpoint")
	if err := os.WriteFile(markerPath, []byte(`[{"chat_id":"chat-1","dump_id":"missing"}]`), 0600); err != nil {
		t.Fatalf("write restore marker: %v", err)
	}
	s := NewScheduler(
		SessionEnv{
			Cfg: &config.Config{
				Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
				Tool:      config.ToolConfig{MaxOutputChars: 1000},
			},
			Rt:            nil,
			Agent:         NewAgent(nil, nil),
			Skills:        nil,
			BrowserBroker: browser.NewNoopBroker(),
		},
		inbox.NewMemory[events.AgentEvent](1),
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected normal cancellation after restore failure, got %v", err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("expected restore marker retention: %v", err)
	}
}

func TestWorkerResumeLoadsConversation(t *testing.T) {
	sessionDir := t.TempDir()
	worker := newConfigTestWorker(&config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	})
	id := uuid.NewString()
	conv := llm.Conversation{
		Messages: []llm.ChatMessage{
			{Role: "user", Content: "old question"},
			{Role: "assistant", Content: "old answer"},
		},
		TotalTokens: 42,
	}
	data, err := json.Marshal(conv)
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, id+".json"), data, 0600); err != nil {
		t.Fatalf("write conversation: %v", err)
	}

	if err := worker.processResume(context.Background(), events.ResumeCommand{ID: id}); err != nil {
		t.Fatalf("resume conversation: %v", err)
	}

	dumpPath := filepath.Join(sessionDir, "after.json")
	if err := worker.loop.DumpConversation(dumpPath); err != nil {
		t.Fatalf("dump resumed conversation: %v", err)
	}
	var got llm.Conversation
	dumped, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dumped conversation: %v", err)
	}
	if err := json.Unmarshal(dumped, &got); err != nil {
		t.Fatalf("unmarshal dumped conversation: %v", err)
	}
	if len(got.Messages) != 2 || got.Messages[0].Content != "old question" || got.Messages[1].Content != "old answer" || got.TotalTokens != 42 {
		t.Fatalf("unexpected resumed conversation: %#v", got)
	}
}

func TestWorkerDumpWritesUUIDConversation(t *testing.T) {
	sessionDir := t.TempDir()
	worker := newConfigTestWorker(&config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	})
	id := uuid.NewString()

	if err := worker.processDump(context.Background(), events.DumpCommand{ID: id}); err != nil {
		t.Fatalf("dump conversation: %v", err)
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

	msg, err := worker.Control.Receive(context.Background())
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
	msg, err := worker.Control.Receive(context.Background())
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
	msg, err = worker.Control.Receive(context.Background())
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

	msg, err := worker.Control.Receive(context.Background())
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
		msg, err := worker.Control.Receive(context.Background())
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
