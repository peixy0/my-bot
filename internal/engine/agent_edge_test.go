package engine

import (
	"context"
	"testing"

	"my-bot/internal/config"
	"my-bot/internal/llm"
	"my-bot/internal/tools"
)

func TestAgent_EmptyContentSendsContinue(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{Content: "", FinishReason: "stop", TotalTokens: 10},
			{Content: "final answer", FinishReason: "stop", TotalTokens: 20},
		},
	}

	agent := NewAgent(client, nil)
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("hello"))
	orch := newMockOrchestrator()

	err := agent.Run(context.Background(), nil, &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(client.calls))
	}

	if len(client.calls[1].messages) != 2 {
		t.Fatalf("expected only 2 messages in second call, got %d", len(client.calls[1].messages))
	}
	if client.calls[1].messages[1].Role != "user" {
		t.Fatalf("expected user message, got %s", client.calls[1].messages[1].Role)
	}
	continueContent, _ := client.calls[1].messages[1].Content.(string)
	if continueContent != "hello" {
		t.Fatalf("expected same user message 'hello', got %q", continueContent)
	}

	if len(orch.finalResponses) != 1 || orch.finalResponses[0] != "final answer" {
		t.Fatalf("expected final response, got %v", orch.finalResponses)
	}
}

func TestAgent_CompressWithFewerThanTwoMessages(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{Content: "done", FinishReason: "stop", TotalTokens: 10},
		},
	}

	agent := NewAgent(client, nil)

	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("only one message"))

	cfg := &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}

	err := agent.Compress(context.Background(), cfg, conv)
	if err != nil {
		t.Fatalf("expected nil error for short conversation, got %v", err)
	}

	if len(conv.Messages) != 1 {
		t.Fatalf("expected 1 message unchanged, got %d", len(conv.Messages))
	}
}

func TestAgent_CompressWithExactlyTwoMessages(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{Content: "anchor summary", FinishReason: "stop", TotalTokens: 50},
		},
	}

	agent := NewAgent(client, nil)

	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("question"))
	conv.Messages = append(conv.Messages, llm.ChatMessage{Role: "assistant", Content: "answer"})

	cfg := &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}

	err := agent.Compress(context.Background(), cfg, conv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
