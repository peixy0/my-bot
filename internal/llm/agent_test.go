package llm

import (
	"context"
	"sync"
	"testing"
	"time"

	"my-bot/internal/config"
	"my-bot/internal/tools"
)

type mockClient struct {
	responses []CompletionResponse
	calls     []mockCompletionCall
	callCount int
	abortCh   chan struct{}
}

type mockCompletionCall struct {
	model       string
	messages    []ChatMessage
	tools       []map[string]any
	maxTokens   int64
	temperature float64
}

func (m *mockClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	m.calls = append(m.calls, mockCompletionCall{
		model:       req.Model,
		messages:    cloneMessages(req.Messages),
		tools:       cloneTools(req.Tools),
		maxTokens:   req.MaxTokens,
		temperature: req.Temperature,
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

	if m.abortCh != nil && idx == 0 {
		<-ctx.Done()
		return resp, context.Canceled
	}

	return resp, nil
}

func cloneMessages(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(messages))
	copy(out, messages)
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
func (o *mockOrchestrator) DispatchTools(_ context.Context, calls []ToolCall) ([]ChatMessage, error) {
	o.dispatches++
	msgs := make([]ChatMessage, len(calls))
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

	agent := NewAgent(client, nil)
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("do something"))
	orch := newMockOrchestrator()

	cfg := &config.Config{LLM: config.LLMConfig{Model: "test-model"}, Context: config.ContextConfig{MaxOutputTokens: 16384}}
	err := agent.Run(context.Background(), nil, cfg, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.calls[0].maxTokens != cfg.Context.MaxOutputTokens {
		t.Fatalf("expected request max_tokens %d, got %d", cfg.Context.MaxOutputTokens, client.calls[0].maxTokens)
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
	if assistantMsg.ReasoningContent != "private chain" {
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

	agent := NewAgent(client, nil)
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("hi"))
	orch := newMockOrchestrator()

	err := agent.Run(context.Background(), nil, &config.Config{LLM: config.LLMConfig{Model: "test-model"}, Context: config.ContextConfig{MaxOutputTokens: 16384}}, "sys", conv, orch, tools.NewRegistry())
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

	agent := NewAgent(client, nil)
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("start"))
	orch := newMockOrchestrator()

	client.responses = append(client.responses[:1],
		CompletionResponse{Content: "anchor summary", FinishReason: "stop", TotalTokens: 200},
		CompletionResponse{Content: "final", FinishReason: "stop", TotalTokens: 300},
	)

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Model:       "test-model",
			Temperature: 1.5,
		},
		Context: config.ContextConfig{
			AutoCompression:      true,
			WindowTokens:         8000,
			MaxOutputTokens:      4096,
			CompressionThreshold: 1.0,
		},
	}
	err := agent.Run(context.Background(), nil, cfg, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundAnchor := false
	for _, msg := range conv.Messages {
		if content, ok := msg.Content.(string); ok {
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
	if client.calls[0].maxTokens != cfg.Context.MaxOutputTokens {
		t.Fatalf("expected initial max_tokens %d, got %d", cfg.Context.MaxOutputTokens, client.calls[0].maxTokens)
	}
	if compressCall.maxTokens != cfg.Context.MaxOutputTokens {
		t.Fatalf("expected compression max_tokens %d, got %d", cfg.Context.MaxOutputTokens, compressCall.maxTokens)
	}
	if compressCall.temperature != compressionTemperature {
		t.Fatalf("expected compression temperature %v, got %v", compressionTemperature, compressCall.temperature)
	}
	if len(compressCall.messages) != 3 {
		t.Fatalf("expected system + evicted user + instruction, got %d messages", len(compressCall.messages))
	}
	if compressCall.messages[0].Role != "system" || compressCall.messages[0].Content != "sys" {
		t.Fatalf("expected compression to reuse active system prompt, got %#v", compressCall.messages[0])
	}
	if compressCall.messages[1].Role != "user" || compressCall.messages[1].Content != "start" {
		t.Fatalf("expected original user message in compression call, got %#v", compressCall.messages[1])
	}
	instruction, _ := compressCall.messages[2].Content.(string)
	if compressCall.messages[2].Role != "user" || instruction == "" {
		t.Fatalf("expected final compression instruction, got %#v", compressCall.messages[2])
	}

	finalCall := client.calls[2]
	if len(finalCall.messages) < 4 {
		t.Fatalf("expected final call to include anchor, assistant tool call, and tool result, got %d messages", len(finalCall.messages))
	}
	if finalCall.messages[1].Role != "user" {
		t.Fatalf("expected anchor as first conversation message, got %#v", finalCall.messages[1])
	}
	if finalCall.messages[2].Role != "assistant" || finalCall.messages[2].ToolCalls == nil {
		t.Fatalf("expected retained assistant tool call before tool result, got %#v", finalCall.messages[2])
	}
	if finalCall.messages[3].Role != "tool" || finalCall.messages[3].ToolCallID != "c1" {
		t.Fatalf("expected tool result after retained assistant tool call, got %#v", finalCall.messages[3])
	}
}

func TestAgent_AbortDuringCompletion(t *testing.T) {
	abortCh := make(chan struct{})

	client := &mockClient{
		responses: []CompletionResponse{
			{Content: "aborted", FinishReason: "stop"},
		},
		abortCh: abortCh,
	}

	agent := NewAgent(client, nil)
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("hello"))
	orch := newMockOrchestrator()

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(abortCh)
	}()

	err := agent.Run(context.Background(), abortCh, &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}, "sys", conv, orch, tools.NewRegistry())

	if err != ErrAborted {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
	wg.Wait()
}

func TestAgent_AbortDuringToolExecution(t *testing.T) {
	abortCh := make(chan struct{})

	client := &mockClient{
		responses: []CompletionResponse{
			{
				Content:      "call tool",
				FinishReason: "tool_calls",
				ToolCalls:    []ToolCall{{ID: "c1", Name: "t", Args: []byte(`{}`)}},
			},
			{Content: "should not reach", FinishReason: "stop"},
		},
		abortCh: abortCh,
	}

	agent := NewAgent(client, nil)
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("start"))
	orch := &mockOrchestratorWithAbort{
		mockOrchestrator: mockOrchestrator{registry: tools.NewRegistry()},
		abortCh:          abortCh,
	}

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		close(abortCh)
	}()

	err := agent.Run(context.Background(), abortCh, &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}, "sys", conv, orch, tools.NewRegistry())

	if err != ErrAborted {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
	wg.Wait()

	if len(client.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(client.calls))
	}
}

func TestAgent_AbortNoActiveCompletion(t *testing.T) {
	agent := NewAgent(&mockClient{
		responses: []CompletionResponse{
			{Content: "ok", FinishReason: "stop"},
		},
	}, nil)
	conv := NewConversation()
	conv.Messages = append(conv.Messages, userMessage("hi"))
	orch := newMockOrchestrator()

	err := agent.Run(context.Background(), nil, &config.Config{
		LLM:     config.LLMConfig{Model: "test"},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
	}, "sys", conv, orch, tools.NewRegistry())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

type mockOrchestratorWithAbort struct {
	mockOrchestrator
	abortCh chan struct{}
}

func (o *mockOrchestratorWithAbort) DispatchTools(_ context.Context, calls []ToolCall) ([]ChatMessage, error) {
	result, err := o.mockOrchestrator.DispatchTools(context.Background(), calls)
	go func() {
		select {
		case o.abortCh <- struct{}{}:
		default:
		}
	}()
	return result, err
}
