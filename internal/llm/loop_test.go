package llm

import (
	"os"
	"path/filepath"
	"testing"

	"my-bot/internal/config"
)

func TestAgentLoopDumpAndLoadConversation(t *testing.T) {
	loop := NewAgentLoop(&config.Config{}, NewAgent(nil))
	loop.conv.Messages = []Message{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "hi"},
	}

	path := filepath.Join(t.TempDir(), "nested", "session.json")
	if err := loop.DumpConversation(path); err != nil {
		t.Fatalf("dump conversation: %v", err)
	}

	loaded := NewAgentLoop(&config.Config{}, NewAgent(nil))
	if err := loaded.LoadConversation(path); err != nil {
		t.Fatalf("load conversation: %v", err)
	}

	if len(loaded.conv.Messages) != 2 {
		t.Fatalf("expected 2 loaded messages, got %d", len(loaded.conv.Messages))
	}
	if loaded.conv.Messages[0]["role"] != "user" || loaded.conv.Messages[0]["content"] != "hello" {
		t.Fatalf("unexpected first message: %#v", loaded.conv.Messages[0])
	}
	if loaded.conv.Messages[1]["role"] != "assistant" || loaded.conv.Messages[1]["content"] != "hi" {
		t.Fatalf("unexpected second message: %#v", loaded.conv.Messages[1])
	}
}

func TestAgentLoopLoadConversationInvalidJSONDoesNotOverwrite(t *testing.T) {
	loop := NewAgentLoop(&config.Config{}, NewAgent(nil))
	loop.conv.Messages = []Message{{"role": "user", "content": "keep me"}}

	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"not":"a message array"}`), 0600); err != nil {
		t.Fatalf("write bad conversation: %v", err)
	}

	if err := loop.LoadConversation(path); err == nil {
		t.Fatal("expected invalid conversation JSON to fail")
	}
	if len(loop.conv.Messages) != 1 || loop.conv.Messages[0]["content"] != "keep me" {
		t.Fatalf("conversation was overwritten: %#v", loop.conv.Messages)
	}
}
