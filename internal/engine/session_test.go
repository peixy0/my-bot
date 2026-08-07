package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"my-bot/internal/browser"
	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/llm"
	"my-bot/internal/tools"
)

func TestNewChatSessionOwnsWorkerResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := newChatSession(
		ctx,
		"chat-1",
		SessionEnv{
			Cfg:           &config.Config{Tool: config.ToolConfig{MaxOutputChars: 1000}},
			Rt:            nil,
			Agent:         NewAgent(nil, nil),
			Skills:        nil,
			BrowserBroker: browser.NewNoopBroker(),
		},
		nil,
	)
	defer func() {
		session.close()
		if err := session.wait(context.Background()); err != nil {
			t.Errorf("wait session: %v", err)
		}
	}()

	if session.tools == nil {
		t.Fatal("expected session tools to be initialized")
	}
	if session.worker == nil {
		t.Fatal("expected session worker to be initialized")
	}
	if session.worker.tools != session.tools {
		t.Fatal("expected worker to borrow session tools")
	}
	if session.cron != nil {
		t.Fatal("expected cron worker to be absent without loader")
	}
}

func TestChatSessionSnapshotsAndRestoresConversation(t *testing.T) {
	ctx := context.Background()
	sessionDir := t.TempDir()
	session := newChatSession(
		ctx,
		"chat-1",
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
		nil,
	)
	defer func() {
		session.close()
		if err := session.wait(context.Background()); err != nil {
			t.Errorf("wait session: %v", err)
		}
	}()

	session.worker.loop.conv = &llm.Conversation{
		Messages:    []llm.ChatMessage{{Role: "user", Content: "remember me"}},
		TotalTokens: 42,
	}
	if err := session.snapshot(ctx, "snapshot"); err != nil {
		t.Fatalf("snapshot conversation: %v", err)
	}
	session.worker.loop.ResetConv()
	if err := session.restore(ctx, "snapshot"); err != nil {
		t.Fatalf("restore conversation: %v", err)
	}
	if len(session.worker.loop.conv.Messages) != 1 || session.worker.loop.conv.Messages[0].Content != "remember me" || session.worker.loop.conv.TotalTokens != 42 {
		t.Fatalf("unexpected restored conversation: %#v", session.worker.loop.conv)
	}
}

func TestChatSessionStopAndSnapshotIdleWorker(t *testing.T) {
	sessionDir := t.TempDir()
	session := newChatSession(
		context.Background(),
		"chat-1",
		SessionEnv{
			Cfg: &config.Config{
				Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
				Tool:      config.ToolConfig{MaxOutputChars: 1000},
			},
			Agent:         NewAgent(nil, nil),
			BrowserBroker: browser.NewNoopBroker(),
		},
		nil,
	)
	session.worker.loop.conv = &llm.Conversation{
		Messages: []llm.ChatMessage{{Role: "user", Content: "remember me"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.stopAndSnapshot(ctx, "reboot"); err != nil {
		t.Fatalf("stop and snapshot: %v", err)
	}
	if err := session.wait(ctx); err != nil {
		t.Fatalf("wait session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "reboot.json")); err != nil {
		t.Fatalf("expected reboot snapshot: %v", err)
	}
}

func TestChatSessionStopAndSnapshotAbortsActiveWorker(t *testing.T) {
	sessionDir := t.TempDir()
	client := &workerRecordingClient{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	session := newChatSession(
		context.Background(),
		"chat-1",
		SessionEnv{
			Cfg: &config.Config{
				Workspace: config.WorkspaceConfig{SessionDir: sessionDir},
				LLM:       config.LLMConfig{Model: "test"},
				Context:   config.ContextConfig{MaxOutputTokens: 100},
				Tool:      config.ToolConfig{MaxOutputChars: 1000},
			},
			Rt:            &nullRuntime{},
			Agent:         NewAgent(client, nil),
			Skills:        tools.NewSkillLoader(""),
			BrowserBroker: browser.NewNoopBroker(),
		},
		nil,
	)
	session.publishMessage(events.TextInputEvent{MessageID: "m1", Message: "working", Sender: &captureOutbound{}})
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("completion did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.stopAndSnapshot(ctx, "active"); err != nil {
		t.Fatalf("stop and snapshot: %v", err)
	}
	if err := session.wait(ctx); err != nil {
		t.Fatalf("wait session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "active.json")); err != nil {
		t.Fatalf("expected active snapshot: %v", err)
	}
}
