package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"my-bot/internal/browser"
	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"
)

type mockSender struct {
	sent   []string
	begins int
	deltas []string
	finals int
	meta   []*events.ResponseMetadata
}

func (s *mockSender) Send(_ context.Context, msg string) {
	s.sent = append(s.sent, msg)
}
func (s *mockSender) SendFull(_ context.Context, msg string, _ *events.ResponseMetadata) {
	s.sent = append(s.sent, msg)
}
func (s *mockSender) SendBegin(_ context.Context) {
	s.begins++
}
func (s *mockSender) SendDelta(_ context.Context, msg string) {
	s.deltas = append(s.deltas, msg)
}
func (s *mockSender) SendFinal(_ context.Context, metadata *events.ResponseMetadata) {
	s.finals++
	s.meta = append(s.meta, metadata)
}
func (s *mockSender) StartThinking(_ context.Context) {}
func (s *mockSender) EndThinking(_ context.Context)   {}

func TestHumanInputOrchestrator_DrainInLoopInputTextOnly(t *testing.T) {
	sender := &mockSender{}
	inLoopInbox := inbox.NewMemory[events.MessageEvent](32)
	orch := NewHumanInputOrchestrator(sender, inLoopInbox)

	inLoopInbox.TryPublish(events.TextInputEvent{ChatID: "chat", MessageID: "m1", Message: "stop doing that"})
	inLoopInbox.TryPublish(events.TextInputEvent{ChatID: "chat", MessageID: "m2", Message: "do this instead"})

	interrupt := orch.MaybeInterrupt(context.Background())
	if interrupt == nil {
		t.Fatalf("expected in-loop interrupt message")
	}
	if interrupt.Role != "user" {
		t.Errorf("expected in-loop input role=user, got %v", interrupt.Role)
	}
	content, ok := interrupt.Content.(string)
	if !ok {
		t.Fatalf("expected text content, got %T", interrupt.Content)
	}
	if !strings.Contains(content, "stop doing that") || !strings.Contains(content, "do this instead") {
		t.Fatalf("expected drained text inputs in content, got %q", content)
	}
	if !strings.Contains(content, "MESSAGE ID: m1") || !strings.Contains(content, "MESSAGE ID: m2") {
		t.Fatalf("expected message IDs in content, got %q", content)
	}
}

func TestHumanInputOrchestrator_DrainInLoopInputWithImageUsesMultipart(t *testing.T) {
	sender := &mockSender{}
	inLoopInbox := inbox.NewMemory[events.MessageEvent](32)
	orch := NewHumanInputOrchestrator(sender, inLoopInbox).WithVision(true)

	inLoopInbox.TryPublish(events.TextInputEvent{ChatID: "chat", MessageID: "m1", Message: "stop doing that"})
	inLoopInbox.TryPublish(events.ImageInputEvent{ChatID: "chat", MessageID: "m2", Message: "see image", ImageData: []events.ImageData{
		{Data: []byte{1, 2, 3}, MIMEType: "image/png"},
		{Data: []byte{4, 5, 6}, MIMEType: "image/jpeg"},
	}})

	interrupt := orch.MaybeInterrupt(context.Background())
	if interrupt == nil {
		t.Fatalf("expected in-loop interrupt message")
	}
	if interrupt.Role != "user" {
		t.Errorf("expected in-loop input role=user, got %v", interrupt.Role)
	}
	content, ok := interrupt.Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected multimodal content, got %T", interrupt.Content)
	}
	if len(content) != 3 {
		t.Fatalf("expected text plus image content, got %d parts", len(content))
	}
	if content[1]["type"] != "image_url" {
		t.Fatalf("expected final content part to be image_url, got %+v", content[1])
	}
	if content[2]["type"] != "image_url" {
		t.Fatalf("expected final content part to be image_url, got %+v", content[2])
	}
	text := content[0]["text"].(string)
	if !strings.Contains(text, "MESSAGE ID: m1") || !strings.Contains(text, "MESSAGE ID: m2") {
		t.Fatalf("expected message IDs before images, got %q", text)
	}
}

func TestBackgroundOrchestrator_NoReport(t *testing.T) {
	sender := &mockSender{}
	orch := NewBackgroundOrchestrator(sender)

	orch.OnContentFinal(context.Background(), nil)
	if len(sender.deltas) != 0 || sender.finals != 0 {
		t.Errorf("expected no send for non-terminal background content, got deltas=%v finals=%d", sender.deltas, sender.finals)
	}

	orch.OnFinalResponse(context.Background(), "all done NO_REPORT", nil)
	if len(sender.sent) != 0 || len(sender.deltas) != 0 || sender.finals != 0 {
		t.Errorf("expected no send for NO_REPORT, got sent=%v deltas=%v finals=%d", sender.sent, sender.deltas, sender.finals)
	}

	orch.OnFinalResponse(context.Background(), "hello world", nil)
	if len(sender.sent) != 1 || sender.sent[0] != "hello world" {
		t.Errorf("expected 'hello world' sent by batch, got %v", sender.sent)
	}
}

func TestHumanInputOrchestrator_StreamsDeltasAndFinal(t *testing.T) {
	sender := &mockSender{}
	orch := NewHumanInputOrchestrator(sender, nil)

	orch.OnContentBegin(context.Background())
	orch.OnContentDelta(context.Background(), "hel")
	orch.OnContentDelta(context.Background(), "lo")
	metadata := &events.ResponseMetadata{Model: "m"}
	orch.OnContentFinal(context.Background(), metadata)

	if fmt.Sprint(sender.deltas) != fmt.Sprint([]string{"hel", "lo"}) {
		t.Fatalf("expected streamed deltas, got %v", sender.deltas)
	}
	if sender.begins != 1 {
		t.Fatalf("expected one begin marker, got %d", sender.begins)
	}
	if sender.finals != 1 {
		t.Fatalf("expected one final stream marker, got %d", sender.finals)
	}
	if len(sender.meta) != 1 || sender.meta[0] != metadata {
		t.Fatalf("expected metadata to reach sender, got %+v", sender.meta)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("expected no duplicate batch send, got %v", sender.sent)
	}
}

func TestHumanInputOrchestrator_ContentFinalClosesStream(t *testing.T) {
	sender := &mockSender{}
	orch := NewHumanInputOrchestrator(sender, nil)

	orch.OnContentFinal(context.Background(), nil)

	if len(sender.deltas) != 0 {
		t.Fatalf("expected no fallback delta, got %v", sender.deltas)
	}
	if sender.finals != 1 {
		t.Fatalf("expected one final marker, got %d", sender.finals)
	}
}

func TestHumanInputOrchestrator_AppendsToolDescription(t *testing.T) {
	sender := &mockSender{}
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSchema{Name: "read_file"}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Description: "reading file example.txt...",
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.TextResult("content"), nil
			},
		}, nil
	})
	orch := NewHumanInputOrchestrator(sender, nil)

	prepared := prepareToolCalls(reg, []llm.ToolCall{{ID: "c1", Name: "read_file", Args: []byte(`{}`)}})
	orch.BeforeToolUse(context.Background(), "using tool", toolDescriptions(prepared))
	if _, err := orch.DispatchTools(context.Background(), prepared); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if fmt.Sprint(sender.deltas) != fmt.Sprint([]string{"\n", "reading file example.txt..."}) {
		t.Fatalf("unexpected tool description delta: %v", sender.deltas)
	}
}

func TestHumanInputOrchestrator_PreparesBatchBeforeDescriptionsAndExecution(t *testing.T) {
	sender := &mockSender{}
	reg := tools.NewRegistry()
	var preparedCount atomic.Int32
	var executedCount atomic.Int32

	register := func(name, description string) {
		reg.Register(tools.ToolSchema{Name: name, Parallel: true}, func([]byte) (tools.PreparedTool, error) {
			preparedCount.Add(1)
			return tools.PreparedTool{
				Description: description,
				Execute: func(context.Context) (tools.ToolResult, error) {
					if preparedCount.Load() != 2 {
						return tools.ToolResult{}, fmt.Errorf("executed before batch preparation completed")
					}
					if len(sender.deltas) != 1 {
						return tools.ToolResult{}, fmt.Errorf("executed before descriptions were sent")
					}
					executedCount.Add(1)
					return tools.TextResult("done"), nil
				},
			}, nil
		})
	}
	register("first", "running first...")
	register("second", "running second...")

	orch := NewHumanInputOrchestrator(sender, nil)
	prepared := prepareToolCalls(reg, []llm.ToolCall{
		{ID: "c1", Name: "first", Args: []byte(`{}`)},
		{ID: "c2", Name: "second", Args: []byte(`{}`)},
	})
	orch.BeforeToolUse(context.Background(), "thinking\n", toolDescriptions(prepared))
	outcomes, err := orch.DispatchTools(context.Background(), prepared)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if preparedCount.Load() != 2 || executedCount.Load() != 2 {
		t.Fatalf("expected two preparations and executions, got prepared=%d executed=%d", preparedCount.Load(), executedCount.Load())
	}
	for i, outcome := range outcomes {
		if outcome.Err != nil {
			t.Fatalf("outcome %d: %v", i, outcome.Err)
		}
	}
	if fmt.Sprint(sender.deltas) != fmt.Sprint([]string{"running first...\nrunning second..."}) {
		t.Fatalf("unexpected description delta: %v", sender.deltas)
	}
}

func TestHumanInputOrchestrator_PreservesEmptyDescriptions(t *testing.T) {
	sender := &mockSender{}
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSchema{Name: "empty"}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.TextResult("done"), nil
			},
		}, nil
	})
	reg.Register(tools.ToolSchema{Name: "invalid"}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{}, fmt.Errorf("missing required argument")
	})
	orch := NewHumanInputOrchestrator(sender, nil)

	prepared := prepareToolCalls(reg, []llm.ToolCall{
		{ID: "c1", Name: "empty", Args: []byte(`{}`)},
		{ID: "c2", Name: "invalid", Args: []byte(`{}`)},
		{ID: "c3", Name: "missing", Args: []byte(`{}`)},
	})
	orch.BeforeToolUse(context.Background(), "", toolDescriptions(prepared))
	outcomes, err := orch.DispatchTools(context.Background(), prepared)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if fmt.Sprint(sender.deltas) != fmt.Sprint([]string{"Calling unknown tool: \"missing\""}) {
		t.Fatalf("unexpected descriptions: %v", sender.deltas)
	}
	if outcomes[1].Err == nil {
		t.Fatal("expected preparation error for invalid tool call")
	}
	if outcomes[2].Err == nil {
		t.Fatalf("expected unknown tool preparation error, got %#v", outcomes[2])
	}
}

func TestBackgroundOrchestrator_DoesNotEmitDescriptions(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSchema{Name: "tool"}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Description: "running tool...",
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.TextResult("done"), nil
			},
		}, nil
	})
	sender := &mockSender{}
	prepared := prepareToolCalls(reg, []llm.ToolCall{{ID: "c1", Name: "tool", Args: []byte(`{}`)}})
	background := NewBackgroundOrchestrator(sender)
	background.BeforeToolUse(context.Background(), "thinking", toolDescriptions(prepared))
	if _, err := background.DispatchTools(context.Background(), prepared); err != nil {
		t.Fatalf("background dispatch: %v", err)
	}
	if len(sender.deltas) != 0 || len(sender.sent) != 0 {
		t.Fatalf("expected no description output, got deltas=%v sent=%v", sender.deltas, sender.sent)
	}
}

func TestSubagentOrchestrator_EmitsDescriptions(t *testing.T) {
	manager := tasks.NewManager(&nullRuntime{}, 1000)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()

	started, err := manager.Start(context.Background(), tasks.StartOptions{
		Description: "subagent descriptions",
		Driver: tasks.FuncDriver(func(_ context.Context, _ tasks.TaskInfo, emit *tasks.Emitter) (tasks.Controller, error) {
			orch := NewSubagentOrchestrator(emit, nil)
			orch.BeforeToolUse(context.Background(), "thinking", []string{"running tool..."})
			emit.Complete(tasks.TaskResult{})
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	snapshot, err := manager.Await(context.Background(), started.TaskID, time.Second)
	if err != nil {
		t.Fatalf("await task: %v", err)
	}
	if snapshot.Output != "\nrunning tool..." {
		t.Fatalf("unexpected subagent description output: %q", snapshot.Output)
	}

	NewSubagentOrchestrator(nil, nil).BeforeToolUse(context.Background(), "thinking", []string{"running tool..."})
}

func TestSubagentToolset_DoesNotForwardSubagentToolContent(t *testing.T) {
	client := &mockClient{
		responses: []llm.CompletionResponse{
			{
				Content:      "using tool",
				FinishReason: "tool_calls",
				ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "missing", Args: []byte(`{}`)}},
				TotalTokens:  10,
			},
			{Content: "subagent result", FinishReason: "stop", TotalTokens: 20},
		},
	}
	agent := NewAgent(client, nil)
	skills := tools.NewSkillLoader("")
	rt := &nullRuntime{}
	cfg := &config.Config{}
	reg := tools.NewRegistry()

	manager := tasks.NewManager(rt, 1000)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()

	NewSubagentToolset(SessionEnv{Cfg: cfg, Rt: rt, Agent: agent, Skills: skills, BrowserBroker: browser.NewNoopBroker()}, manager).Register(reg)

	preparer, ok := reg.Get("agent")
	if !ok {
		t.Fatal("expected agent preparer")
	}
	prepared, err := preparer([]byte(`{"name":"worker-one","task":"do work","system_prompt":"sys"}`))
	if err != nil {
		t.Fatalf("prepare agent: %v", err)
	}
	result, err := prepared.Execute(context.Background())
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
	if snap.Description != "worker-one" {
		t.Fatalf("expected agent name as task description, got %q", snap.Description)
	}
	if !strings.Contains(snap.Output, "subagent result") {
		t.Fatalf("expected subagent output, got %+v", snap)
	}
}

func TestFleetToolset_ReturnsAllChildTaskIDs(t *testing.T) {
	agent := NewAgent(&echoTaskClient{}, nil)
	skills := tools.NewSkillLoader("")
	rt := &nullRuntime{}
	cfg := &config.Config{Tool: config.ToolConfig{MaxOutputChars: 1000}}
	reg := tools.NewRegistry()
	manager := tasks.NewManager(rt, 1000)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()

	NewFleetToolset(SessionEnv{Cfg: cfg, Rt: rt, Agent: agent, Skills: skills, BrowserBroker: browser.NewNoopBroker()}, manager).Register(reg)

	preparer, ok := reg.Get("fleet")
	if !ok {
		t.Fatal("expected fleet preparer")
	}
	prepared, err := preparer([]byte(`{"system_prompt":"sys","tasks":[{"name":"agent-a","task":"a"},{"name":"agent-b","task":"b"},{"name":"agent-c","task":"c"}]}`))
	if err != nil {
		t.Fatalf("prepare fleet: %v", err)
	}

	result, err := prepared.Execute(context.Background())
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
	wantNames := map[string]struct{}{"agent-a": {}, "agent-b": {}, "agent-c": {}}
	for _, id := range start.TaskIDs {
		snap, err := manager.Await(context.Background(), id, 2*time.Second)
		if err != nil {
			t.Fatalf("await fleet child task %s: %v", id, err)
		}
		if _, ok := wantNames[snap.Description]; !ok {
			t.Fatalf("unexpected fleet task description %q", snap.Description)
		}
		delete(wantNames, snap.Description)
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

func (c *echoTaskClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	content := ""
	if len(req.Messages) > 0 {
		content, _ = req.Messages[len(req.Messages)-1].Content.(string)
	}
	result := "result:" + content
	req.OnContentDelta(ctx, result)
	return llm.CompletionResponse{Content: result, FinishReason: "stop", TotalTokens: 10}, nil
}

func TestRunDispatch_UnknownTool(t *testing.T) {
	reg := tools.NewRegistry()
	calls := []llm.ToolCall{{ID: "c1", Name: "nonexistent", Args: []byte(`{}`)}}

	outcomes, err := runDispatch(context.Background(), prepareToolCalls(reg, calls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(outcomes))
	}
	if outcomes[0].Err == nil || !strings.Contains(outcomes[0].Err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool preparation error, got %#v", outcomes[0])
	}
}

func TestRunDispatch_ImageToolInjectsMultimodalUserMessage(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSchema{Name: "read_image"}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Description: "reading image...",
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.ImageResult([]map[string]any{
					{
						"type": "image_url",
						"image_url": map[string]any{
							"url":    "data:image/png;base64,AAAA",
							"detail": "auto",
						},
					},
				}), nil
			},
		}, nil
	})

	outcomes, err := runDispatch(context.Background(), prepareToolCalls(reg, []llm.ToolCall{{ID: "c1", Name: "read_image", Args: []byte(`{}`)}}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(outcomes))
	}
	if outcomes[0].Followup == nil {
		t.Fatalf("expected follow-up user multimodal message, got nil")
	}
	if outcomes[0].ToolMsg.Role != "tool" {
		t.Fatalf("expected tool reply role=tool, got %#v", outcomes[0].ToolMsg)
	}
	toolContent, _ := outcomes[0].ToolMsg.Content.(string)
	if !strings.Contains(toolContent, "non-text content") {
		t.Fatalf("expected tool placeholder text, got %q", toolContent)
	}
	if outcomes[0].Followup.Role != "user" {
		t.Fatalf("expected follow-up role=user, got %#v", outcomes[0].Followup)
	}
	content, ok := outcomes[0].Followup.Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected multimodal content, got %T", outcomes[0].Followup.Content)
	}
	if len(content) != 2 {
		t.Fatalf("expected text preface plus image block, got %d parts", len(content))
	}
	if content[0]["type"] != "text" || content[1]["type"] != "image_url" {
		t.Fatalf("expected text then image blocks, got %+v", content)
	}
}

func TestRunDispatch_MultipleToolsAppendFollowupsAfterAllToolReplies(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSchema{Name: "read_image", Parallel: true}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Description: "reading image...",
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.ImageResult([]map[string]any{
					{
						"type": "image_url",
						"image_url": map[string]any{
							"url":    "data:image/png;base64,AAAA",
							"detail": "auto",
						},
					},
				}), nil
			},
		}, nil
	})
	reg.Register(tools.ToolSchema{Name: "read_file", Parallel: true}, func([]byte) (tools.PreparedTool, error) {
		return tools.PreparedTool{
			Description: "reading file...",
			Execute: func(context.Context) (tools.ToolResult, error) {
				return tools.TextResult("file contents"), nil
			},
		}, nil
	})

	outcomes, err := runDispatch(context.Background(), prepareToolCalls(reg, []llm.ToolCall{
		{ID: "c1", Name: "read_image", Args: []byte(`{}`)},
		{ID: "c2", Name: "read_file", Args: []byte(`{}`)},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(outcomes))
	}
	for i, oc := range outcomes {
		if oc.ToolMsg.Role != "tool" {
			t.Fatalf("outcome %d: expected tool reply, got %#v", i, oc.ToolMsg)
		}
	}
	if outcomes[0].Followup == nil {
		t.Fatalf("expected follow-up user message from read_image outcome")
	}
	if outcomes[0].Followup.Role != "user" {
		t.Fatalf("expected follow-up role=user, got %#v", outcomes[0].Followup)
	}
}

func TestRunDispatch_RespectsParallelFlag(t *testing.T) {
	run := func(t *testing.T, parallel bool) {
		t.Helper()
		reg := tools.NewRegistry()
		started := make(chan string, 2)
		release := make(chan struct{}, 2)
		for _, name := range []string{"first", "second"} {
			name := name
			reg.Register(tools.ToolSchema{Name: name, Parallel: parallel}, func([]byte) (tools.PreparedTool, error) {
				return tools.PreparedTool{
					Description: "running " + name,
					Execute: func(context.Context) (tools.ToolResult, error) {
						started <- name
						<-release
						return tools.TextResult("done"), nil
					},
				}, nil
			})
		}
		done := make(chan []llm.CallOutcome, 1)
		go func() {
			outcomes, _ := runDispatch(context.Background(), prepareToolCalls(reg, []llm.ToolCall{
				{ID: "c1", Name: "first", Args: []byte(`{}`)},
				{ID: "c2", Name: "second", Args: []byte(`{}`)},
			}))
			done <- outcomes
		}()

		<-started
		if parallel {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("parallel tools did not overlap")
			}
			release <- struct{}{}
			release <- struct{}{}
		} else {
			select {
			case name := <-started:
				t.Fatalf("sequential tool %s started before the first completed", name)
			case <-time.After(20 * time.Millisecond):
			}
			release <- struct{}{}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("second sequential tool did not start")
			}
			release <- struct{}{}
		}
		outcomes := <-done
		for i, outcome := range outcomes {
			if outcome.Err != nil {
				t.Fatalf("outcome %d: %v", i, outcome.Err)
			}
		}
	}

	t.Run("parallel", func(t *testing.T) {
		run(t, true)
	})
	t.Run("sequential", func(t *testing.T) {
		run(t, false)
	})
}

type nullRuntime struct{}

func (r *nullRuntime) Truncate(_ context.Context, text string, _ int) string {
	return text
}
func (r *nullRuntime) TruncateTail(_ context.Context, text string, _ int) string {
	return text
}
func (r *nullRuntime) ExecuteTruncated(_ context.Context, _ io.Reader, _ ...string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (r *nullRuntime) Execute(_ context.Context, _ io.Reader, _ ...string) (runtime.ExecResult, error) {
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
func (r *nullRuntime) ReadFileRange(_ context.Context, _ string, _ int64, _ int) (runtime.ReadFileRangeResult, error) {
	return runtime.ReadFileRangeResult{}, nil
}
func (r *nullRuntime) WriteFile(_ context.Context, _, _ string) error           { return nil }
func (r *nullRuntime) WriteTmpFile(_ context.Context, _ string) (string, error) { return "", nil }
func (r *nullRuntime) AppendFile(_ context.Context, _, _ string) error          { return nil }
func (r *nullRuntime) Glob(_ context.Context, _ string, _ int) (runtime.GlobResult, error) {
	return runtime.GlobResult{}, nil
}
func (r *nullRuntime) EditFile(_ context.Context, _ string, _ []runtime.Edit) error { return nil }
func (r *nullRuntime) OSInfo(_ context.Context) (string, error) {
	return "OS: linux/amd64\nWorking directory: /workspace", nil
}

func TestSubagentRegistry_HasNoMetaTools(t *testing.T) {
	rt := &nullRuntime{}
	skills := tools.NewSkillLoader("")
	cfg := &config.Config{}
	manager := tasks.NewManager(rt, 1000)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	cmdTools := tools.NewCommandToolset(rt, manager)
	browserBroker := browser.NewNoopBroker()
	browserClient := browserBroker.NewClient()

	reg := NewSubagentRegistry(rt, skills, cfg, cmdTools, browserClient)

	if _, ok := reg.Schema("agent"); ok {
		t.Error("subagent registry should not expose 'agent' meta-tool")
	}
	if _, ok := reg.Schema("fleet"); ok {
		t.Error("subagent registry should not expose 'fleet' meta-tool")
	}
}
