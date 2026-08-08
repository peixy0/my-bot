package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/llm"
	"my-bot/internal/tools"
)

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type mockClient struct {
	responses []llm.CompletionResponse
	calls     []mockCompletionCall
	callCount int
	abortCh   chan struct{}
}

type mockCompletionCall struct {
	model       string
	messages    []llm.ChatMessage
	tools       []map[string]any
	maxTokens   int64
	temperature float64
}

func (m *mockClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
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
		return llm.CompletionResponse{FinishReason: "stop"}, nil
	}
	resp := m.responses[idx]
	if req.OnContentBegin != nil {
		req.OnContentBegin(ctx)
	}
	if req.OnContentDelta != nil && resp.Content != "" {
		req.OnContentDelta(ctx, resp.Content)
	}

	if m.abortCh != nil && idx == 0 {
		<-ctx.Done()
		return resp, context.Canceled
	}

	return resp, nil
}

func cloneMessages(messages []llm.ChatMessage) []llm.ChatMessage {
	out := make([]llm.ChatMessage, len(messages))
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
	registry         *tools.Registry
	begins           int
	finalResponses   []string
	toolDescriptions [][]string
	dispatchContents []string
	dispatches       int
	outcomes         []llm.CallOutcome
	interrupt        *llm.ChatMessage
	finalMetadata    []*events.ResponseMetadata
}

func newMockOrchestrator() *mockOrchestrator {
	return &mockOrchestrator{registry: tools.NewRegistry()}
}

func (o *mockOrchestrator) OnContentBegin(context.Context) {
	o.begins++
}
func (o *mockOrchestrator) OnContentDelta(context.Context, string) {}
func (o *mockOrchestrator) OnContentFinal(_ context.Context, metadata *events.ResponseMetadata) {
	o.finalMetadata = append(o.finalMetadata, metadata)
}
func (o *mockOrchestrator) OnFinalResponse(ctx context.Context, content string, metadata *events.ResponseMetadata) {
	o.finalResponses = append(o.finalResponses, content)
}
func (o *mockOrchestrator) BeforeToolUse(ctx context.Context, content string, descriptions []string) {
	o.dispatchContents = append(o.dispatchContents, content)
	o.toolDescriptions = append(o.toolDescriptions, descriptions)
}
func (o *mockOrchestrator) DispatchTools(ctx context.Context, calls []preparedToolCall) ([]llm.CallOutcome, error) {
	o.dispatches++
	if o.outcomes != nil {
		return o.outcomes, nil
	}
	out := make([]llm.CallOutcome, len(calls))
	for i, tc := range calls {
		out[i] = llm.CallOutcome{ToolMsg: llm.ToolResultMessage(tc.call.ID, "result")}
	}
	return out, nil
}

func (o *mockOrchestrator) MaybeInterrupt(context.Context) *llm.ChatMessage {
	return o.interrupt
}

func TestAgent_ToolCallDispatch(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{
				Content:          "thinking",
				ReasoningContent: "private chain",
				FinishReason:     "tool_calls",
				ToolCalls:        []llm.ToolCall{{ID: "call1", Name: "test", Args: []byte(`{}`)}},
				TotalTokens:      50,
			},
			{
				Content:          "final",
				FinishReason:     "stop",
				PromptTokens:     80,
				CompletionTokens: 20,
				TotalTokens:      100,
				GenerationTime:   2 * time.Second,
			},
		},
	}

	agent := NewAgent(client, nil)
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("do something"))
	orch := newMockOrchestrator()
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSchema{Name: "test"}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Description: "running test",
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.TextResult("done"), nil
			},
		}, nil
	})

	cfg := &config.Config{
		LLM:     config.LLMConfig{Model: "test-model", ContextWindow: 1000},
		Context: config.ContextConfig{MaxOutputTokens: 16384},
		Tool:    config.ToolConfig{EnableDescriptiveOutput: true},
	}
	err := agent.Run(context.Background(), nil, cfg, "sys", conv, orch, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.calls[0].maxTokens != cfg.Context.MaxOutputTokens {
		t.Fatalf("expected request max_tokens %d, got %d", cfg.Context.MaxOutputTokens, client.calls[0].maxTokens)
	}
	if orch.dispatches != 1 {
		t.Errorf("expected 1 dispatch, got %d", orch.dispatches)
	}
	if len(orch.finalMetadata) != 2 || orch.finalMetadata[0] != nil || orch.finalMetadata[1] == nil {
		t.Fatalf("expected metadata only for the final response, got %+v", orch.finalMetadata)
	}
	if metadata := orch.finalMetadata[1]; metadata.Model != "test-model" || metadata.PromptTokens != 80 || metadata.CompletionTokens != 20 || metadata.TotalTokens != 100 || metadata.ContextWindow != 1000 || metadata.GenerationTime != 2*time.Second {
		t.Fatalf("unexpected final metadata: %+v", metadata)
	}
	if len(orch.dispatchContents) != 1 || orch.dispatchContents[0] != "thinking" {
		t.Fatalf("expected original response content at dispatch, got %v", orch.dispatchContents)
	}
	if fmt.Sprint(orch.toolDescriptions) != fmt.Sprint([][]string{{"running test"}}) {
		t.Fatalf("expected prepared tool description, got %v", orch.toolDescriptions)
	}
	if len(client.calls) < 2 || len(client.calls[1].messages) < 3 {
		t.Fatalf("expected second call to include prior assistant message, got %+v", client.calls)
	}
	assistantMsg := client.calls[1].messages[2]
	if assistantMsg.ReasoningContent != "private chain" {
		t.Fatalf("expected reasoning_content in replayed assistant message, got %#v", assistantMsg)
	}
	if assistantMsg.Content != "thinking" {
		t.Fatalf("expected original response content in replayed assistant message, got %#v", assistantMsg.Content)
	}
}

func TestAgent_OmitsMetadataForContentlessToolResponse(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{
				FinishReason: "tool_calls",
				ToolCalls:    []llm.ToolCall{{ID: "call1", Name: "test", Args: []byte(`{}`)}},
				TotalTokens:  50,
			},
			{Content: "final", FinishReason: "stop", TotalTokens: 100},
		},
	}
	orch := newMockOrchestrator()
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSchema{Name: "test"}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.TextResult("done"), nil
			},
		}, nil
	})

	err := NewAgent(client, nil).Run(
		context.Background(),
		nil,
		&config.Config{LLM: config.LLMConfig{Model: "test-model"}},
		"sys",
		llm.NewConversation(),
		orch,
		reg,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(orch.finalMetadata) != 2 || orch.finalMetadata[0] != nil || orch.finalMetadata[1] == nil {
		t.Fatalf("expected metadata only for final content-bearing response, got %+v", orch.finalMetadata)
	}
}

func TestAgent_AppendsToolMessagesBeforeFollowupsAndInterrupts(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{
				Content:      "thinking",
				FinishReason: "tool_calls",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "read_image", Args: []byte(`{}`)},
					{ID: "c2", Name: "read_file", Args: []byte(`{}`)},
				},
				TotalTokens: 10,
			},
			{Content: "final", FinishReason: "stop", TotalTokens: 20},
		},
	}
	followup := llm.UserMessage("image payload")
	interrupt := llm.UserMessage("user interrupt")
	orch := newMockOrchestrator()
	orch.outcomes = []llm.CallOutcome{
		{ToolMsg: llm.ToolResultMessage("c1", "image placeholder"), Followup: &followup},
		{ToolMsg: llm.ToolResultMessage("c2", "file contents")},
	}
	orch.interrupt = &interrupt
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("inspect files"))

	cfg := &config.Config{LLM: config.LLMConfig{Model: "test-model"}, Context: config.ContextConfig{MaxOutputTokens: 16384}}
	err := NewAgent(client, nil).Run(context.Background(), nil, cfg, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.calls) < 2 {
		t.Fatalf("expected second LLM call, got %d calls", len(client.calls))
	}
	messages := client.calls[1].messages
	if len(messages) < 7 {
		t.Fatalf("expected system, user, assistant, two tools, followup, interrupt; got %+v", messages)
	}
	if messages[2].Role != "assistant" || len(messages[2].ToolCalls) != 2 {
		t.Fatalf("expected assistant with two tool calls, got %#v", messages[2])
	}
	if messages[3].Role != "tool" || messages[3].ToolCallID != "c1" {
		t.Fatalf("expected first tool result for c1, got %#v", messages[3])
	}
	if messages[4].Role != "tool" || messages[4].ToolCallID != "c2" {
		t.Fatalf("expected second tool result for c2, got %#v", messages[4])
	}
	if messages[5].Role != "user" || messages[5].Content != "image payload" {
		t.Fatalf("expected followup after all tool results, got %#v", messages[5])
	}
	if messages[6].Role != "user" || messages[6].Content != "user interrupt" {
		t.Fatalf("expected interrupt after followup, got %#v", messages[6])
	}
}

func TestAgent_SkipsOnlyFailedToolOutcomes(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{
				Content:      "thinking",
				FinishReason: "tool_calls",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "bad", Args: []byte(`{}`)},
					{ID: "c2", Name: "good", Args: []byte(`{}`)},
				},
				TotalTokens: 10,
			},
			{Content: "final", FinishReason: "stop", TotalTokens: 20},
		},
	}
	orch := newMockOrchestrator()
	orch.outcomes = []llm.CallOutcome{
		{Err: context.Canceled},
		{ToolMsg: llm.ToolResultMessage("c2", "ok")},
	}
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("run tools"))

	cfg := &config.Config{LLM: config.LLMConfig{Model: "test-model", SkipOnToolDispatchError: true}, Context: config.ContextConfig{MaxOutputTokens: 16384}}
	err := NewAgent(client, nil).Run(context.Background(), nil, cfg, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages := client.calls[1].messages
	if len(messages[2].ToolCalls) != 1 || messages[2].ToolCalls[0].ID != "c2" {
		t.Fatalf("expected assistant replay to include only successful c2 tool call, got %#v", messages[2].ToolCalls)
	}
	if messages[3].Role != "tool" || messages[3].ToolCallID != "c2" {
		t.Fatalf("expected only c2 tool result, got %#v", messages[3])
	}
}

func TestAgent_ToolCallContentDoesNotBlockDispatch(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{
				Content:      "msg",
				FinishReason: "tool_calls",
				ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "t", Args: []byte(`{}`)}},
				TotalTokens:  10,
			},
			{Content: "ok", FinishReason: "stop", TotalTokens: 20},
		},
	}

	agent := NewAgent(client, nil)
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("hi"))
	orch := newMockOrchestrator()

	cfg := &config.Config{LLM: config.LLMConfig{Model: "test-model"}, Context: config.ContextConfig{MaxOutputTokens: 16384}}
	err := agent.Run(context.Background(), nil, cfg, "sys", conv, orch, tools.NewRegistry())
	if err != nil {
		t.Fatalf("error should not propagate, got: %v", err)
	}

	if orch.dispatches != 1 {
		t.Errorf("expected tool dispatch, got %d dispatches", orch.dispatches)
	}
}

func TestAgent_CompressionOnHighTokens(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{
				Content:      "big response",
				FinishReason: "tool_calls",
				ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "x", Args: []byte(`{}`)}},
				TotalTokens:  9000,
			},
			{Content: "compressed result", FinishReason: "stop", TotalTokens: 500},
		},
	}

	agent := NewAgent(client, nil)
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("start"))
	orch := newMockOrchestrator()

	client.responses = append(client.responses[:1],
		llm.CompletionResponse{Content: "anchor summary", FinishReason: "stop", TotalTokens: 200},
		llm.CompletionResponse{Content: "final", FinishReason: "stop", TotalTokens: 300},
	)

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Model:         "test-model",
			Temperature:   1.5,
			ContextWindow: 8000,
		},
		Context: config.ContextConfig{
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
	if compressCall.temperature != cfg.LLM.Temperature {
		t.Fatalf("expected compression temperature %v, got %v", cfg.LLM.Temperature, compressCall.temperature)
	}
	if len(compressCall.messages) != 2 {
		t.Fatalf("expected system + flattened user, got %d messages", len(compressCall.messages))
	}
	sysContent, _ := compressCall.messages[0].Content.(string)
	if compressCall.messages[0].Role != "system" || sysContent != llm.CompressionInstruction {
		t.Fatalf("expected compression system prompt to be compressionInstruction, got role=%q content[:50]=%q", compressCall.messages[0].Role, truncateStr(sysContent, 50))
	}
	flatContent, _ := compressCall.messages[1].Content.(string)
	if compressCall.messages[1].Role != "user" || flatContent == "" {
		t.Fatalf("expected flattened conversation text as user message, got %#v", compressCall.messages[1])
	}
	if !strings.Contains(flatContent, "[USER]:") {
		t.Fatalf("expected flattened text to contain [USER]: tag, got %q", truncateStr(flatContent, 100))
	}
	if !strings.Contains(flatContent, "[ASSISTANT]:") {
		t.Fatalf("expected flattened text to contain [ASSISTANT]: tag, got %q", truncateStr(flatContent, 100))
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
		responses: []llm.CompletionResponse{
			{Content: "aborted", FinishReason: "stop"},
		},
		abortCh: abortCh,
	}

	agent := NewAgent(client, nil)
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("hello"))
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
		responses: []llm.CompletionResponse{
			{
				Content:      "call tool",
				FinishReason: "tool_calls",
				ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "t", Args: []byte(`{}`)}},
			},
			{Content: "should not reach", FinishReason: "stop"},
		},
		abortCh: abortCh,
	}

	agent := NewAgent(client, nil)
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("start"))
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
		responses: []llm.CompletionResponse{
			{Content: "ok", FinishReason: "stop"},
		},
	}, nil)
	conv := llm.NewConversation()
	conv.Messages = append(conv.Messages, llm.UserMessage("hi"))
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

func (o *mockOrchestratorWithAbort) DispatchTools(ctx context.Context, calls []preparedToolCall) ([]llm.CallOutcome, error) {
	result, err := o.mockOrchestrator.DispatchTools(context.Background(), calls)
	go func() {
		select {
		case o.abortCh <- struct{}{}:
		default:
		}
	}()
	return result, err
}

func (o *mockOrchestratorWithAbort) MaybeInterrupt(context.Context) *llm.ChatMessage {
	return nil
}
