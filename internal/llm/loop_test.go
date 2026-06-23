package llm

import (
	"os"
	"path/filepath"
	"testing"

	"my-bot/internal/config"
)

func TestAgentLoopDumpAndLoadConversation(t *testing.T) {
	loop := NewAgentLoop(&config.Config{}, NewAgent(nil, nil))
	loop.conv.Messages = []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	path := filepath.Join(t.TempDir(), "nested", "session.json")
	if err := loop.DumpConversation(path); err != nil {
		t.Fatalf("dump conversation: %v", err)
	}

	loaded := NewAgentLoop(&config.Config{}, NewAgent(nil, nil))
	if err := loaded.LoadConversation(path); err != nil {
		t.Fatalf("load conversation: %v", err)
	}

	if len(loaded.conv.Messages) != 2 {
		t.Fatalf("expected 2 loaded messages, got %d", len(loaded.conv.Messages))
	}
	if loaded.conv.Messages[0].Role != "user" || loaded.conv.Messages[0].Content != "hello" {
		t.Fatalf("unexpected first message: %#v", loaded.conv.Messages[0])
	}
	if loaded.conv.Messages[1].Role != "assistant" || loaded.conv.Messages[1].Content != "hi" {
		t.Fatalf("unexpected second message: %#v", loaded.conv.Messages[1])
	}
}

func TestAgentLoopLoadConversationInvalidJSONDoesNotOverwrite(t *testing.T) {
	loop := NewAgentLoop(&config.Config{}, NewAgent(nil, nil))
	loop.conv.Messages = []ChatMessage{{Role: "user", Content: "keep me"}}

	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"not":"a message array"}`), 0600); err != nil {
		t.Fatalf("write bad conversation: %v", err)
	}

	if err := loop.LoadConversation(path); err == nil {
		t.Fatal("expected invalid conversation JSON to fail")
	}
	if len(loop.conv.Messages) != 1 || loop.conv.Messages[0].Content != "keep me" {
		t.Fatalf("conversation was overwritten: %#v", loop.conv.Messages)
	}
}
