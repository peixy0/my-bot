package engine

import (
	"context"
	"testing"

	"my-bot/internal/browser"
	"my-bot/internal/config"
	"my-bot/internal/llm"
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
