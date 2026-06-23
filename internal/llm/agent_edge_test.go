package llm

import (
	"context"
	"testing"

	"my-bot/internal/config"
	"my-bot/internal/tools"
)

func TestAgent_EmptyContentSendsContinue(t *testing.T) {
	// First response: empty content, no tool calls → should inject "continue"
	// Second response: actual content → should terminate
	client := &mockClient{
		responses: []CompletionResponse{
			{Content: "", FinishReason: "stop", TotalTokens: 10},
			{Content: "final answer", FinishReason: "stop", TotalTokens: 20},
		},
	}

	agent := NewAgent(client, nil)
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("hello"))
	orch := newMockOrchestrator()

	err := agent.Run(context.Background(), nil, &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have made 2 calls: first got empty content, second got "final answer"
	if len(client.calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(client.calls))
	}

	// The second call should include a "continue" user message after the empty assistant response
	// Message order: system + user("hello") + assistant("") + user("continue")
	if len(client.calls[1].messages) < 4 {
		t.Fatalf("expected at least 4 messages in second call, got %d", len(client.calls[1].messages))
	}
	if client.calls[1].messages[2].Role != "assistant" {
		t.Fatalf("expected assistant message before continue, got %s", client.calls[1].messages[2].Role)
	}
	if client.calls[1].messages[3].Role != "user" {
		t.Fatalf("expected user continue message, got %s", client.calls[1].messages[3].Role)
	}
	continueContent, _ := client.calls[1].messages[3].Content.(string)
	if continueContent != "continue" {
		t.Fatalf("expected 'continue' message, got %q", continueContent)
	}

	// Orchestrator should have received the final response
	if len(orch.finalResponses) != 1 || orch.finalResponses[0] != "final answer" {
		t.Fatalf("expected final response, got %v", orch.finalResponses)
	}
}

func TestAgent_CompressWithFewerThanTwoMessages(t *testing.T) {
	client := &mockClient{
		responses: []CompletionResponse{
			{Content: "done", FinishReason: "stop", TotalTokens: 10},
		},
	}

	agent := NewAgent(client, nil)

	// Conversation with only 1 message → Compress should return nil immediately
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("only one message"))

	cfg := &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}

	err := agent.Compress(context.Background(), cfg, "sys", conv)
	if err != nil {
		t.Fatalf("expected nil error for short conversation, got %v", err)
	}

	// Conversation should be unchanged
	if len(conv.Messages) != 1 {
		t.Fatalf("expected 1 message unchanged, got %d", len(conv.Messages))
	}
}

func TestAgent_CompressWithExactlyTwoMessages(t *testing.T) {
	client := &mockClient{
		responses: []CompletionResponse{
			{Content: "anchor summary", FinishReason: "stop", TotalTokens: 50},
		},
	}

	agent := NewAgent(client, nil)

	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("question"))
	conv.Messages = append(conv.Messages, ChatMessage{Role: "assistant", Content: "answer"})

	cfg := &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}

	err := agent.Compress(context.Background(), cfg, "sys", conv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have: [CONTEXT ANCHOR] user msg + retained last message
	if len(conv.Messages) != 2 {
		t.Fatalf("expected 2 messages after compression, got %d", len(conv.Messages))
	}
	anchorContent, _ := conv.Messages[0].Content.(string)
	if anchorContent != "[CONTEXT ANCHOR]\nanchor summary" {
		t.Fatalf("expected anchor message, got %q", anchorContent)
	}
	if conv.Messages[1].Role != "assistant" {
		t.Fatalf("expected retained last message to be assistant, got %s", conv.Messages[1].Role)
	}
}
