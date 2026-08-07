package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"my-bot/internal/browser"
	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/llm"
	"my-bot/internal/tools"
)

type workerRecordingClient struct {
	mu       sync.Mutex
	requests []llm.CompletionRequest
	started  chan struct{}
	release  chan struct{}
}

func (c *workerRecordingClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	if c.started != nil {
		c.started <- struct{}{}
	}
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return llm.CompletionResponse{}, ctx.Err()
		}
	}
	if req.OnContentBegin != nil {
		req.OnContentBegin(ctx)
	}
	if req.OnContentDelta != nil {
		req.OnContentDelta(ctx, "done")
	}
	return llm.CompletionResponse{Content: "done", FinishReason: "stop", TotalTokens: 1}, nil
}

func (c *workerRecordingClient) snapshot() []llm.CompletionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]llm.CompletionRequest(nil), c.requests...)
}

func newWorkerForMessageTest(t *testing.T, client llm.CompletionClient) *ConversationWorker {
	t.Helper()
	cfg := &config.Config{
		Workspace: config.WorkspaceConfig{SessionDir: t.TempDir()},
		LLM:       config.LLMConfig{Model: "test", Vision: true},
		Context:   config.ContextConfig{MaxOutputTokens: 100},
		Tool:      config.ToolConfig{MaxOutputChars: 1000},
	}
	env := SessionEnv{
		Cfg:           cfg,
		Rt:            &nullRuntime{},
		Agent:         NewAgent(client, nil),
		Skills:        tools.NewSkillLoader(""),
		BrowserBroker: browser.NewNoopBroker(),
	}
	sessionTools := newSessionTools(env)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := sessionTools.Shutdown(ctx); err != nil {
			t.Errorf("shutdown tools: %v", err)
		}
	})
	return newConversationWorker("chat-1", env, sessionTools)
}

func TestConversationWorkerBatchesBufferedMessages(t *testing.T) {
	client := &workerRecordingClient{started: make(chan struct{}, 1)}
	worker := newWorkerForMessageTest(t, client)
	first := &captureOutbound{}
	second := &captureOutbound{}
	worker.MessageInbox.TryPublish(events.TextInputEvent{MessageID: "m1", Message: "first", Sender: first})
	worker.MessageInbox.TryPublish(events.TextInputEvent{MessageID: "m2", Message: "second", Sender: second})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx)
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start completion")
	}
	cancel()
	<-done

	requests := client.snapshot()
	if len(requests) != 1 {
		t.Fatalf("expected one completion request, got %d", len(requests))
	}
	content, ok := requests[0].Messages[len(requests[0].Messages)-1].Content.(string)
	if !ok {
		t.Fatalf("expected text content, got %T", requests[0].Messages[len(requests[0].Messages)-1].Content)
	}
	if !strings.Contains(content, "m1") || !strings.Contains(content, "m2") {
		t.Fatalf("expected both messages in batch, got %q", content)
	}
	if len(first.messages) == 0 || len(second.messages) != 0 {
		t.Fatalf("expected first sender only, first=%v second=%v", first.messages, second.messages)
	}
}

func TestConversationWorkerProcessesMessageArrivingDuringCompletionNext(t *testing.T) {
	client := &workerRecordingClient{
		started: make(chan struct{}, 2),
		release: make(chan struct{}, 2),
	}
	worker := newWorkerForMessageTest(t, client)
	sender := &captureOutbound{}
	worker.MessageInbox.TryPublish(events.TextInputEvent{MessageID: "m1", Message: "first", Sender: sender})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx)
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("first completion did not start")
	}
	worker.MessageInbox.TryPublish(events.TextInputEvent{MessageID: "m2", Message: "second", Sender: sender})
	client.release <- struct{}{}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("second completion did not start")
	}
	client.release <- struct{}{}
	cancel()
	<-done

	if len(client.snapshot()) != 2 {
		t.Fatalf("expected two completion requests, got %d", len(client.snapshot()))
	}
}

func TestConversationWorkerAbortKeepsPendingMessages(t *testing.T) {
	client := &workerRecordingClient{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	worker := newWorkerForMessageTest(t, client)
	sender := &captureOutbound{}
	worker.MessageInbox.TryPublish(events.TextInputEvent{MessageID: "m1", Message: "first", Sender: sender})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx)
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("first completion did not start")
	}
	worker.MessageInbox.TryPublish(events.TextInputEvent{MessageID: "m2", Message: "second", Sender: sender})
	select {
	case worker.abortCh <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("abort was not received")
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("pending message was not processed after abort")
	}
	cancel()
	<-done

	if len(client.snapshot()) != 2 {
		t.Fatalf("expected pending message to start a second request, got %d", len(client.snapshot()))
	}
}
