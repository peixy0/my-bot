package engine

import (
	"context"
	"testing"

	"my-bot/internal/config"
	"my-bot/internal/llm"
)

func TestNewChatSessionOwnsWorkerResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := newChatSession(
		ctx,
		"chat-1",
		&config.Config{Tool: config.ToolConfig{MaxOutputChars: 1000}},
		llm.NewAgent(nil, nil),
		nil,
		nil,
		nil,
	)
	defer session.close()

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
