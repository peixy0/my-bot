package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/runtime"
	"my-bot/internal/tools"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

func wireMetaTools(r *tools.Registry, agent *Agent, skills *tools.SkillLoader, cfg *config.Config, rt runtime.Runtime, _ events.Outbound) {
	NewSubagentToolset(agent, skills, cfg, rt).Register(r)
	NewFleetToolset(agent, skills, cfg, rt).Register(r)
}

type Orchestrator interface {
	Wire(r *tools.Registry)
	OnContentDelta(ctx context.Context, delta string)
	OnContentFinal(ctx context.Context, content string)
	OnFinalResponse(ctx context.Context, content string)
	BeforeToolUse(ctx context.Context, content string)
	DispatchTools(ctx context.Context, calls []ToolCall) ([]Message, error)
}

func sendBeforeToolUse(ctx context.Context, sender events.Outbound, content string) {
	if sender == nil {
		return
	}
	content = strings.TrimSpace(content)
	if content != "" {
		sender.Send(ctx, content)
	}
}

type HumanInputOrchestrator struct {
	registry    *tools.Registry
	sender      events.Outbound
	inLoopInbox inbox.Inbox[events.WorkerEvent]
	agent       *Agent
	skills      *tools.SkillLoader
	cfg         *config.Config
	rt          runtime.Runtime
}

func NewHumanInputOrchestrator(
	sender events.Outbound,
	inLoopInbox inbox.Inbox[events.WorkerEvent],
	agent *Agent,
	skills *tools.SkillLoader,
	cfg *config.Config,
	rt runtime.Runtime,
) *HumanInputOrchestrator {
	return &HumanInputOrchestrator{sender: sender, inLoopInbox: inLoopInbox, agent: agent, skills: skills, cfg: cfg, rt: rt}
}

func (o *HumanInputOrchestrator) Wire(r *tools.Registry) {
	if registrar, ok := o.sender.(tools.ToolRegistrar); ok {
		registrar.RegisterTools(r)
	}
	wireMetaTools(r, o.agent, o.skills, o.cfg, o.rt, o.sender)
	o.registry = r
}

func (o *HumanInputOrchestrator) OnContentDelta(ctx context.Context, delta string) {
	if o.sender == nil || delta == "" {
		return
	}
	o.sender.SendDelta(ctx, delta)
}

func (o *HumanInputOrchestrator) OnContentFinal(ctx context.Context, _ string) {
	if o.sender == nil {
		return
	}
	o.sender.SendFinal(ctx)
}

func (o *HumanInputOrchestrator) OnFinalResponse(_ context.Context, _ string) {}

func (o *HumanInputOrchestrator) BeforeToolUse(_ context.Context, _ string) {}

func (o *HumanInputOrchestrator) DispatchTools(ctx context.Context, calls []ToolCall) ([]Message, error) {

	toolMsgs, err := runDispatch(ctx, o.registry, calls)
	if err != nil {
		return nil, err
	}
	if inject := o.drainInLoopInput(); inject != nil {
		slog.Debug("in-loop user input injected after tool dispatch", "tools", len(calls))
		toolMsgs = append(toolMsgs, *inject)
	}
	return toolMsgs, nil
}

func (o *HumanInputOrchestrator) drainInLoopInput() *Message {
	if o.inLoopInbox == nil {
		return nil
	}
	var items []events.WorkerEvent
	for {
		msg, ok := o.inLoopInbox.TryReceive()
		if !ok {
			break
		}
		items = append(items, msg.Payload)
	}
	if len(items) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("[USER MESSAGE INTERRUPTING YOUR CURRENT WORK]\n")
	sb.WriteString("The user sent additional message while you were working. Read carefully and adjust your plan immediately if it changes what you should do next:\n\n")
	var imageParts []map[string]any
	for _, item := range items {
		switch ev := item.(type) {
		case events.TextInputEvent:
			sb.WriteString(ev.Message)
			sb.WriteString("\n\n")
		case events.ImageInputEvent:
			if !o.cfg.VisionSupport {
				continue
			}
			if strings.TrimSpace(ev.Message) != "" {
				sb.WriteString(ev.Message)
				sb.WriteString("\n\n")
			}
			imageParts = append(imageParts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url":    fmt.Sprintf("data:%s;base64,%s", ev.MIMEType, base64.StdEncoding.EncodeToString(ev.ImageData)),
					"detail": "auto",
				},
			})
		}
	}
	text := strings.TrimSpace(sb.String())
	if len(imageParts) == 0 {
		msg := userMessage(text)
		return &msg
	}
	parts := []map[string]any{{"type": "text", "text": text}}
	parts = append(parts, imageParts...)
	msg := Message{"role": "user", "content": parts}
	return &msg
}

type BackgroundOrchestrator struct {
	registry *tools.Registry
	sender   events.Outbound
	agent    *Agent
	skills   *tools.SkillLoader
	cfg      *config.Config
	rt       runtime.Runtime
}

func NewBackgroundOrchestrator(
	sender events.Outbound,
	agent *Agent,
	skills *tools.SkillLoader,
	cfg *config.Config,
	rt runtime.Runtime,
) *BackgroundOrchestrator {
	return &BackgroundOrchestrator{sender: sender, agent: agent, skills: skills, cfg: cfg, rt: rt}
}

func (o *BackgroundOrchestrator) Wire(r *tools.Registry) {
	wireMetaTools(r, o.agent, o.skills, o.cfg, o.rt, nil)
	o.registry = r
}

func (o *BackgroundOrchestrator) OnContentDelta(_ context.Context, _ string) {}

func (o *BackgroundOrchestrator) OnContentFinal(_ context.Context, _ string) {}

func (o *BackgroundOrchestrator) OnFinalResponse(ctx context.Context, content string) {
	const noReport = "NO_REPORT"
	if o.sender == nil || content == "" || strings.HasSuffix(strings.TrimSpace(content), noReport) {
		return
	}
	o.sender.Send(ctx, content)
}

func (o *BackgroundOrchestrator) BeforeToolUse(_ context.Context, _ string) {}

func (o *BackgroundOrchestrator) DispatchTools(ctx context.Context, calls []ToolCall) ([]Message, error) {
	return runDispatch(ctx, o.registry, calls)
}

type SubagentOrchestrator struct {
	registry *tools.Registry
	output   string
}

func NewSubagentOrchestrator() *SubagentOrchestrator {
	return &SubagentOrchestrator{}
}

func (o *SubagentOrchestrator) Wire(r *tools.Registry) { o.registry = r }

func (o *SubagentOrchestrator) OnContentDelta(_ context.Context, _ string) {}

func (o *SubagentOrchestrator) OnContentFinal(_ context.Context, _ string) {}

func (o *SubagentOrchestrator) OnFinalResponse(_ context.Context, content string) {
	o.output = content
}

func (o *SubagentOrchestrator) BeforeToolUse(_ context.Context, _ string) {}

func (o *SubagentOrchestrator) Output() string { return o.output }

func (o *SubagentOrchestrator) DispatchTools(ctx context.Context, calls []ToolCall) ([]Message, error) {
	return runDispatch(ctx, o.registry, calls)
}

type SubagentToolset struct {
	runner *subagentRunner
}

func NewSubagentToolset(agent *Agent, skills *tools.SkillLoader, cfg *config.Config, rt runtime.Runtime) *SubagentToolset {
	return &SubagentToolset{runner: newSubagentRunner(agent, skills, cfg, rt)}
}

type FleetToolset struct {
	runner *subagentRunner
}

func NewFleetToolset(agent *Agent, skills *tools.SkillLoader, cfg *config.Config, rt runtime.Runtime) *FleetToolset {
	return &FleetToolset{runner: newSubagentRunner(agent, skills, cfg, rt)}
}

type subagentRunner struct {
	agent  *Agent
	skills *tools.SkillLoader
	cfg    *config.Config
	rt     runtime.Runtime
}

func newSubagentRunner(agent *Agent, skills *tools.SkillLoader, cfg *config.Config, rt runtime.Runtime) *subagentRunner {
	return &subagentRunner{agent: agent, skills: skills, cfg: cfg, rt: rt}
}

type subagentTaskMessage struct {
	AgentID      string
	Task         string
	SystemPrompt string
}

type subagentResultMessage struct {
	AgentID string
	Output  string
	Error   string
}

func newSubagentTask(systemPrompt, task string) subagentTaskMessage {
	return subagentTaskMessage{
		AgentID:      uuid.NewString(),
		Task:         task,
		SystemPrompt: systemPrompt,
	}
}

func (r *subagentRunner) run(ctx context.Context, systemPrompt, task string) (subagentResultMessage, error) {
	return runSubagentTask(ctx, r.agent, r.skills, r.cfg, r.rt, newSubagentTask(systemPrompt, task))
}

func (r *subagentRunner) runMany(ctx context.Context, systemPrompt string, tasks []string) []string {
	results := make([]string, len(tasks))
	g, gctx := errgroup.WithContext(ctx)
	for i, task := range tasks {
		i, task := i, task
		g.Go(func() error {
			result, err := r.run(gctx, systemPrompt, task)
			if err != nil {
				results[i] = fmt.Sprintf("error: %v", err)
				return nil
			}
			results[i] = result.Output
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		slog.Error("fleet: errgroup failed", "err", err)
	}
	return results
}

func runSubagentTask(
	ctx context.Context,
	agent *Agent,
	skills *tools.SkillLoader,
	cfg *config.Config,
	rt runtime.Runtime,
	task subagentTaskMessage,
) (subagentResultMessage, error) {
	result := subagentResultMessage{
		AgentID: task.AgentID,
	}
	cmdTools := tools.NewCommandToolset(rt, cfg.ToolMaxOutputChars)
	defer func() {
		ctx2, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = cmdTools.Shutdown(ctx2)
	}()
	reg := tools.NewRegistry()
	reg.RegisterToolset(tools.NewDefaultToolset(rt, skills, cfg))
	reg.RegisterToolset(cmdTools)
	orch := NewSubagentOrchestrator()
	loop := NewAgentLoop(cfg, agent, cmdTools)
	if err := loop.Run(ctx, reg, orch, NewSubagentPrompt(skills, rt, task.SystemPrompt), task.Task); err != nil {
		result.Error = err.Error()
		return result, err
	}
	result.Output = orch.Output()
	return result, nil
}

func (s *SubagentToolset) Register(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "agent",
		Description: "Run an isolated subagent to handle a specific, self-contained task.\n\nThe subagent has access to the same tools and runs with a fresh conversation.\nIt cannot spawn further subagents.\nUse this to delegate well-defined, isolated work units that can be\ncompleted independently of the current conversation context.\nReturns the final text response from the subagent.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The task description for the subagent to execute. Provide full context as the subagent has no conversation history.",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "System prompt for the subagent.",
				},
			},
			"required": []string{"task", "system_prompt"},
		},
	}, func(ctx context.Context, args []byte) (tools.ToolResult, error) {
		var p struct {
			Task         string `json:"task"`
			SystemPrompt string `json:"system_prompt"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return tools.ToolResult{}, err
		}
		slog.Debug("subagent start", "task_len", len(p.Task))
		result, err := s.runner.run(ctx, p.SystemPrompt, p.Task)
		if err != nil {
			slog.Debug("subagent error", "err", err)
			return tools.ToolResult{}, err
		}
		slog.Debug("subagent done", "agent_id", result.AgentID, "output_len", len(result.Output))
		return tools.TextResult(result.Output), nil
	})
}

func (s *FleetToolset) Register(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "fleet",
		Description: "Run multiple isolated subagents in parallel, each handling one task.\n\nAll subagents share the same system prompt but receive independent tasks.\nThey have access to the same tools and run with fresh conversations.\nSubagents cannot spawn further subagents.\nUse this to fan out well-defined, independent work units that can be\nexecuted concurrently.\nReturns a list of outputs, one per task, in the original task order.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "Shared system prompt applied to every subagent. Describe the role or constraints common to all tasks.",
				},
				"tasks": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "List of task descriptions to execute in parallel. Each task is handled by an independent subagent with full context.",
				},
			},
			"required": []string{"system_prompt", "tasks"},
		},
	}, func(ctx context.Context, args []byte) (tools.ToolResult, error) {
		var p struct {
			SystemPrompt string   `json:"system_prompt"`
			Tasks        []string `json:"tasks"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return tools.ToolResult{}, err
		}
		slog.Debug("fleet start", "tasks", len(p.Tasks))
		results := s.runner.runMany(ctx, p.SystemPrompt, p.Tasks)
		slog.Debug("fleet done", "tasks", len(p.Tasks))
		return tools.TextResult(tools.MarshalResult(results)), nil
	})
}

func execOne(ctx context.Context, r *tools.Registry, tc ToolCall) Message {
	handler, ok := r.Handler(tc.Name)
	if !ok {
		return toolResultMessage(tc.ID, fmt.Sprintf("unknown tool %q", tc.Name))
	}
	slog.Debug("tool call start", "tool", tc.Name, "id", tc.ID, "args", string(tc.Args))
	result, err := handler(ctx, tc.Args)
	if err != nil {
		slog.Debug("tool call error", "tool", tc.Name, "id", tc.ID, "err", err)
		return toolResultMessage(tc.ID, fmt.Sprintf("error: %v", err))
	}
	slog.Debug("tool call done", "tool", tc.Name, "id", tc.ID, "result", result.Text)
	if result.Blocks != nil {
		return toolResultBlocksMessage(tc.ID, result.Blocks)
	}
	return toolResultMessage(tc.ID, result.Text)
}

func runDispatch(ctx context.Context, r *tools.Registry, calls []ToolCall) ([]Message, error) {
	msgs := make([]Message, len(calls))

	if len(calls) == 1 {
		msgs[0] = execOne(ctx, r, calls[0])
		return msgs, nil
	}

	var seqMu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for i, tc := range calls {
		i, tc := i, tc
		g.Go(func() error {
			if r.IsParallel(tc.Name) {
				msgs[i] = execOne(gctx, r, tc)
			} else {
				seqMu.Lock()
				msgs[i] = execOne(gctx, r, tc)
				seqMu.Unlock()
			}
			return nil
		})
	}
	return msgs, g.Wait()
}
