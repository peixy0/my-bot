package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"
)

type mockSender struct {
	sent   []string
	deltas []string
	finals int
}

func (s *mockSender) Send(_ context.Context, msg string) {
	s.sent = append(s.sent, msg)
}
func (s *mockSender) SendDelta(_ context.Context, msg string) {
	s.deltas = append(s.deltas, msg)
}
func (s *mockSender) SendFinal(_ context.Context) {
	s.finals++
}
func (s *mockSender) StartThinking(_ context.Context) {}
func (s *mockSender) EndThinking(_ context.Context)   {}

func TestHumanInputOrchestrator_DrainInLoopInputTextOnly(t *testing.T) {
	sender := &mockSender{}
	inLoopInbox := inbox.NewMemory[events.WorkerEvent](32)
	reg := tools.NewRegistry()
	orch := NewHumanInputOrchestrator(reg, sender, inLoopInbox)

	inLoopInbox.TryPublish(inbox.NewEnvelope[events.WorkerEvent]("in-loop", inbox.Address{Kind: "worker", ID: "chat"}, events.TextInputEvent{ChatID: "chat", Message: "stop doing that"}))
	inLoopInbox.TryPublish(inbox.NewEnvelope[events.WorkerEvent]("in-loop", inbox.Address{Kind: "worker", ID: "chat"}, events.TextInputEvent{ChatID: "chat", Message: "do this instead"}))

	calls := []ToolCall{{ID: "c1", Name: "unknown_tool", Args: []byte(`{}`)}}
	msgs, err := orch.DispatchTools(context.Background(), calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (in-loop input + tool result), got %d", len(msgs))
	}

	inputMsg := msgs[1]
	if inputMsg["role"] != "user" {
		t.Errorf("expected in-loop input role=user, got %v", inputMsg["role"])
	}
	content, ok := inputMsg["content"].(string)
	if !ok {
		t.Fatalf("expected text content, got %T", inputMsg["content"])
	}
	if !strings.Contains(content, "stop doing that") || !strings.Contains(content, "do this instead") {
		t.Fatalf("expected drained text inputs in content, got %q", content)
	}
}

func TestHumanInputOrchestrator_DrainInLoopInputWithImageUsesMultipart(t *testing.T) {
	sender := &mockSender{}
	inLoopInbox := inbox.NewMemory[events.WorkerEvent](32)
	reg := tools.NewRegistry()
	orch := NewHumanInputOrchestrator(reg, sender, inLoopInbox).WithVision(true)

	inLoopInbox.TryPublish(inbox.NewEnvelope[events.WorkerEvent]("in-loop", inbox.Address{Kind: "worker", ID: "chat"}, events.TextInputEvent{ChatID: "chat", Message: "stop doing that"}))
	inLoopInbox.TryPublish(inbox.NewEnvelope[events.WorkerEvent]("in-loop", inbox.Address{Kind: "worker", ID: "chat"}, events.ImageInputEvent{ChatID: "chat", Message: "see image", MIMEType: "image/png", ImageData: []byte{1, 2, 3}}))

	calls := []ToolCall{{ID: "c1", Name: "unknown_tool", Args: []byte(`{}`)}}
	msgs, err := orch.DispatchTools(context.Background(), calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (in-loop input + tool result), got %d", len(msgs))
	}

	inputMsg := msgs[1]
	if inputMsg["role"] != "user" {
		t.Errorf("expected in-loop input role=user, got %v", inputMsg["role"])
	}
	content, ok := inputMsg["content"].([]map[string]any)
	if !ok {
		t.Fatalf("expected multimodal content, got %T", inputMsg["content"])
	}
	if len(content) != 2 {
		t.Fatalf("expected text plus image content, got %d parts", len(content))
	}
	if content[1]["type"] != "image_url" {
		t.Fatalf("expected final content part to be image_url, got %+v", content[1])
	}
}

func TestBackgroundOrchestrator_NoReport(t *testing.T) {
	sender := &mockSender{}
	orch := NewBackgroundOrchestrator(tools.NewRegistry(), sender)

	orch.OnContentFinal(context.Background(), "intermediate")
	if len(sender.deltas) != 0 || sender.finals != 0 {
		t.Errorf("expected no send for non-terminal background content, got deltas=%v finals=%d", sender.deltas, sender.finals)
	}

	orch.OnFinalResponse(context.Background(), "all done NO_REPORT")
	if len(sender.sent) != 0 || len(sender.deltas) != 0 || sender.finals != 0 {
		t.Errorf("expected no send for NO_REPORT, got sent=%v deltas=%v finals=%d", sender.sent, sender.deltas, sender.finals)
	}

	orch.OnFinalResponse(context.Background(), "hello world")
	if len(sender.sent) != 1 || sender.sent[0] != "hello world" {
		t.Errorf("expected 'hello world' sent by batch, got %v", sender.sent)
	}
}

func TestHumanInputOrchestrator_StreamsDeltasAndFinal(t *testing.T) {
	sender := &mockSender{}
	orch := NewHumanInputOrchestrator(tools.NewRegistry(), sender, nil)

	orch.OnContentDelta(context.Background(), "hel")
	orch.OnContentDelta(context.Background(), "lo")
	orch.OnContentFinal(context.Background(), "hello")

	if fmt.Sprint(sender.deltas) != fmt.Sprint([]string{"hel", "lo"}) {
		t.Fatalf("expected streamed deltas, got %v", sender.deltas)
	}
	if sender.finals != 1 {
		t.Fatalf("expected one final stream marker, got %d", sender.finals)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("expected no duplicate batch send, got %v", sender.sent)
	}
}

func TestHumanInputOrchestrator_ContentFinalClosesStream(t *testing.T) {
	sender := &mockSender{}
	orch := NewHumanInputOrchestrator(tools.NewRegistry(), sender, nil)

	orch.OnContentFinal(context.Background(), "hello")

	if len(sender.deltas) != 0 {
		t.Fatalf("expected no fallback delta, got %v", sender.deltas)
	}
	if sender.finals != 1 {
		t.Fatalf("expected one final marker, got %d", sender.finals)
	}
}

func TestHumanInputOrchestrator_ContentFinalBeforeToolUse(t *testing.T) {
	sender := &mockSender{}
	orch := NewHumanInputOrchestrator(tools.NewRegistry(), sender, nil)

	orch.OnContentDelta(context.Background(), "using tool")
	orch.OnContentFinal(context.Background(), "using tool")
	orch.BeforeToolUse(context.Background(), "using tool")

	if sender.finals != 1 {
		t.Fatalf("expected stream to be finalized before tool use, got %d", sender.finals)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("expected no batch send after streamed content, got %v", sender.sent)
	}
}

func TestSubagentOrchestrator_DoesNotForwardToolContent(t *testing.T) {
	sender := &mockSender{}
	orch := NewSubagentOrchestrator(tools.NewRegistry(), nil, nil)

	orch.BeforeToolUse(context.Background(), "using tool")

	if len(sender.sent) != 0 {
		t.Fatalf("expected no outbound sends from subagent, got %v", sender.sent)
	}
}

func TestSubagentToolset_DoesNotForwardSubagentToolContent(t *testing.T) {
	client := &mockClient{
		responses: []CompletionResponse{
			{
				Content:      "using tool",
				FinishReason: "tool_calls",
				ToolCalls:    []ToolCall{{ID: "c1", Name: "missing", Args: []byte(`{}`)}},
				TotalTokens:  10,
			},
			{Content: "subagent result", FinishReason: "stop", TotalTokens: 20},
		},
	}
	agent := NewAgent(client)
	skills := tools.NewSkillLoader("")
	rt := &nullRuntime{}
	cfg := &config.Config{}
	reg := tools.NewRegistry()

	manager := tasks.NewManager(1000)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	NewSubagentToolset(agent, skills, cfg, rt, manager).Register(reg)
	handler, ok := reg.Handler("agent")
	if !ok {
		t.Fatal("expected agent handler")
	}

	result, err := handler(context.Background(), []byte(`{"task":"do work","system_prompt":"sys"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var start map[string]string
	if err := json.Unmarshal([]byte(result.Text), &start); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	if start["task_id"] == "" {
		t.Fatalf("expected task_id, got %q", result.Text)
	}
	snap, err := manager.Await(context.Background(), start["task_id"], 2*time.Second)
	if err != nil {
		t.Fatalf("await subagent task: %v", err)
	}
	if !strings.Contains(snap.Output, "subagent result") {
		t.Fatalf("expected subagent output, got %+v", snap)
	}
}

func TestFleetToolset_ReturnsAllChildTaskIDs(t *testing.T) {
	agent := NewAgent(&echoTaskClient{})
	skills := tools.NewSkillLoader("")
	rt := &nullRuntime{}
	cfg := &config.Config{ToolMaxOutputChars: 1000}
	reg := tools.NewRegistry()
	manager := tasks.NewManager(1000)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()

	NewFleetToolset(agent, skills, cfg, rt, manager).Register(reg)
	handler, ok := reg.Handler("fleet")
	if !ok {
		t.Fatal("expected fleet handler")
	}

	result, err := handler(context.Background(), []byte(`{"system_prompt":"sys","tasks":["a","b","c"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var start struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal([]byte(result.Text), &start); err != nil {
		t.Fatalf("unmarshal fleet result: %v\n%s", err, result.Text)
	}
	if len(start.TaskIDs) != 3 {
		t.Fatalf("expected 3 child task ids, got %+v", start)
	}
	for _, taskID := range start.TaskIDs {
		if taskID == "" {
			t.Fatalf("expected non-empty child task ids, got %+v", start)
		}
	}
	wantByTaskID := map[string]string{}
	for _, id := range start.TaskIDs {
		snap, err := manager.Await(context.Background(), id, 2*time.Second)
		if err != nil {
			t.Fatalf("await fleet child task %s: %v", id, err)
		}
		wantByTaskID[id] = snap.Output
	}
	wantParts := []string{"result:a", "result:b", "result:c"}
	for _, part := range wantParts {
		found := false
		for _, output := range wantByTaskID {
			if strings.Contains(output, part) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected some fleet child output to contain %q, got %+v", part, wantByTaskID)
		}
	}
}

type echoTaskClient struct{}

func (c *echoTaskClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	content := ""
	if len(req.Messages) > 0 {
		content, _ = req.Messages[len(req.Messages)-1]["content"].(string)
	}
	result := "result:" + content
	req.OnContentDelta(ctx, result)
	return CompletionResponse{Content: result, FinishReason: "stop", TotalTokens: 10}, nil
}

func TestRunDispatch_UnknownTool(t *testing.T) {
	reg := tools.NewRegistry()
	calls := []ToolCall{{ID: "c1", Name: "nonexistent", Args: []byte(`{}`)}}

	msgs, err := runDispatch(context.Background(), reg, calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	content, _ := msgs[0]["content"].(string)
	if content == "" {
		t.Error("expected error content for unknown tool")
	}
}

type nullRuntime struct{}

func (r *nullRuntime) Truncate(_ context.Context, text string, _ int) string {
	return text
}
func (r *nullRuntime) Execute(_ context.Context, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (r *nullRuntime) Spawn(_ context.Context, _ string) (*runtime.ProcessHandle, error) {
	return nil, nil
}
func (r *nullRuntime) ReadRawBytes(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (r *nullRuntime) ReadFile(_ context.Context, _ string, _, _ int) (runtime.ReadFileResult, error) {
	return runtime.ReadFileResult{}, nil
}
func (r *nullRuntime) WriteFile(_ context.Context, _, _ string) error           { return nil }
func (r *nullRuntime) WriteTmpFile(_ context.Context, _ string) (string, error) { return "", nil }
func (r *nullRuntime) Glob(_ context.Context, _ string) (runtime.GlobResult, error) {
	return runtime.GlobResult{}, nil
}
func (r *nullRuntime) EditFile(_ context.Context, _ string, _ []runtime.Edit) error { return nil }
func (r *nullRuntime) OSInfo(_ context.Context) (string, error) {
	return "OS: linux/amd64\nWorking directory: /workspace", nil
}

func TestForBackground_HasMetaTools(t *testing.T) {
	client := &mockClient{
		responses: []CompletionResponse{
			{Content: "done", FinishReason: "stop", TotalTokens: 10},
		},
	}
	agent := NewAgent(client)
	skills := tools.NewSkillLoader("")
	rt := &nullRuntime{}
	cfg := &config.Config{}

	reg := tools.NewRegistry()
	manager := tasks.NewManager(1000)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	RegisterMetaTools(reg, agent, skills, cfg, rt, manager)

	schemas := reg.Schemas()
	hasAgent := false
	hasFleet := false
	for _, s := range schemas {
		fn, _ := s["function"].(map[string]any)
		if fn != nil {
			name, _ := fn["name"].(string)
			if name == "agent" {
				hasAgent = true
			}
			if name == "fleet" {
				hasFleet = true
			}
		}
	}
	if !hasAgent {
		t.Error("expected background orchestrator to have 'agent' meta-tool")
	}
	if !hasFleet {
		t.Error("expected background orchestrator to have 'fleet' meta-tool")
	}
}
