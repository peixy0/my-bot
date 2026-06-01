package llm

import (
	"context"
	"testing"

	"my-bot/internal/config"
	"my-bot/internal/tools"
)

type mockClient struct {
	responses []CompletionResponse
	calls     []mockCompletionCall
	callCount int
}

type mockCompletionCall struct {
	model    string
	messages []Message
	tools    []map[string]any
}

func (m *mockClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	m.calls = append(m.calls, mockCompletionCall{
		model:    req.Model,
		messages: cloneMessages(req.Messages),
		tools:    cloneTools(req.Tools),
	})
	idx := m.callCount
	m.callCount++
	if idx >= len(m.responses) {
		return CompletionResponse{FinishReason: "stop"}, nil
	}
	resp := m.responses[idx]
	if req.OnContentDelta != nil && resp.Content != "" {
		req.OnContentDelta(ctx, resp.Content)
	}
	return resp, nil
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, msg := range messages {
		c := make(Message, len(msg))
		for k, v := range msg {
			c[k] = v
		}
		out[i] = c
	}
	return out
}

func cloneTools(tools []map[string]any) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, tool := range tools {
		c := make(map[string]any, len(tool))
		for k, v := range tool {
			c[k] = v
		}
		out[i] = c
	}
	return out
}

type mockOrchestrator struct {
	registry        *tools.Registry
	beforeToolCalls int
	finalContent    []string
	finalResponses  []string
	dispatches      int
}

func newMockOrchestrator() *mockOrchestrator {
	return &mockOrchestrator{registry: tools.NewRegistry()}
}

func (o *mockOrchestrator) Wire(_ *tools.Registry)                     {}
func (o *mockOrchestrator) OnContentDelta(_ context.Context, _ string) {}
func (o *mockOrchestrator) OnContentFinal(_ context.Context, content string) {
	o.finalContent = append(o.finalContent, content)
}
func (o *mockOrchestrator) OnFinalResponse(_ context.Context, content string) {
	o.finalResponses = append(o.finalResponses, content)
}
func (o *mockOrchestrator) BeforeToolUse(_ context.Context, _ string) {
	o.beforeToolCalls++
}
func (o *mockOrchestrator) DispatchTools(_ context.Context, calls []ToolCall) ([]Message, error) {
	o.dispatches++
	msgs := make([]Message, len(calls))
	for i, tc := range calls {
		msgs[i] = toolResultMessage(tc.ID, "result")
	}
	return msgs, nil
}

func TestAgent_ToolCallDispatch(t *testing.T) {
	client := &mockClient{
		responses: []CompletionResponse{
			{
				Content:          "thinking",
				ReasoningContent: "private chain",
				FinishReason:     "tool_calls",
				ToolCalls:        []ToolCall{{ID: "call1", Name: "test", Args: []byte(`{}`)}},
				TotalTokens:      50,
			},
			{Content: "final", FinishReason: "stop", TotalTokens: 100},
		},
	}

	agent := NewAgent(client, "test-model")
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("do something"))
	orch := newMockOrchestrator()

	err := agent.Run(context.Background(), &config.Config{}, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if orch.beforeToolCalls != 1 {
		t.Errorf("expected 1 BeforeToolUse call, got %d", orch.beforeToolCalls)
	}
	if orch.dispatches != 1 {
		t.Errorf("expected 1 dispatch, got %d", orch.dispatches)
	}
	if len(client.calls) < 2 || len(client.calls[1].messages) < 3 {
		t.Fatalf("expected second call to include prior assistant message, got %+v", client.calls)
	}
	assistantMsg := client.calls[1].messages[2]
	if assistantMsg["reasoning_content"] != "private chain" {
		t.Fatalf("expected reasoning_content in replayed assistant message, got %#v", assistantMsg)
	}
}

func TestAgent_BeforeToolUseErrorNonFatal(t *testing.T) {
	client := &mockClient{
		responses: []CompletionResponse{
			{
				Content:      "msg",
				FinishReason: "tool_calls",
				ToolCalls:    []ToolCall{{ID: "c1", Name: "t", Args: []byte(`{}`)}},
				TotalTokens:  10,
			},
			{Content: "ok", FinishReason: "stop", TotalTokens: 20},
		},
	}

	agent := NewAgent(client, "test-model")
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("hi"))
	orch := newMockOrchestrator()

	err := agent.Run(context.Background(), &config.Config{}, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("error should not propagate, got: %v", err)
	}

	if orch.dispatches != 1 {
		t.Errorf("expected tool dispatch even after BeforeToolUse error, got %d dispatches", orch.dispatches)
	}
}

func TestAgent_CompressionOnHighTokens(t *testing.T) {
	client := &mockClient{
		responses: []CompletionResponse{
			{
				Content:      "big response",
				FinishReason: "tool_calls",
				ToolCalls:    []ToolCall{{ID: "c1", Name: "x", Args: []byte(`{}`)}},
				TotalTokens:  9000,
			},
			{Content: "compressed result", FinishReason: "stop", TotalTokens: 500},
		},
	}

	agent := NewAgent(client, "test-model")
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("start"))
	orch := newMockOrchestrator()

	client.responses = append(client.responses[:1],
		CompletionResponse{Content: "anchor summary", FinishReason: "stop", TotalTokens: 200},
		CompletionResponse{Content: "final", FinishReason: "stop", TotalTokens: 300},
	)

	cfg := &config.Config{
		ContextAutoCompressionEnabled: true,
		ContextMaxTokens:              8000,
		ContextCompressionThreshold:   1.0,
	}
	err := agent.Run(context.Background(), cfg, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundAnchor := false
	for _, msg := range conv.Messages {
		if content, ok := msg["content"].(string); ok {
			if len(content) > 0 && content[:min(len(content), 16)] == "[CONTEXT ANCHOR]" {
				foundAnchor = true
			}
		}
	}
	if !foundAnchor {
		t.Error("expected compression to produce a [CONTEXT ANCHOR] message")
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(client.calls))
	}

	compressCall := client.calls[1]
	if len(compressCall.tools) != 0 {
		t.Fatalf("expected compression call to disable tools, got %d tools", len(compressCall.tools))
	}
	if len(compressCall.messages) != 3 {
		t.Fatalf("expected system + evicted user + instruction, got %d messages", len(compressCall.messages))
	}
	if compressCall.messages[0]["role"] != "system" || compressCall.messages[0]["content"] != "sys" {
		t.Fatalf("expected compression to reuse active system prompt, got %#v", compressCall.messages[0])
	}
	if compressCall.messages[1]["role"] != "user" || compressCall.messages[1]["content"] != "start" {
		t.Fatalf("expected original user message in compression call, got %#v", compressCall.messages[1])
	}
	instruction, _ := compressCall.messages[2]["content"].(string)
	if compressCall.messages[2]["role"] != "user" || instruction == "" {
		t.Fatalf("expected final compression instruction, got %#v", compressCall.messages[2])
	}

	finalCall := client.calls[2]
	if len(finalCall.messages) < 4 {
		t.Fatalf("expected final call to include anchor, assistant tool call, and tool result, got %d messages", len(finalCall.messages))
	}
	if finalCall.messages[1]["role"] != "user" {
		t.Fatalf("expected anchor as first conversation message, got %#v", finalCall.messages[1])
	}
	if finalCall.messages[2]["role"] != "assistant" || finalCall.messages[2]["tool_calls"] == nil {
		t.Fatalf("expected retained assistant tool call before tool result, got %#v", finalCall.messages[2])
	}
	if finalCall.messages[3]["role"] != "tool" || finalCall.messages[3]["tool_call_id"] != "c1" {
		t.Fatalf("expected tool result after retained assistant tool call, got %#v", finalCall.messages[3])
	}
}
