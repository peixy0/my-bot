package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"my-bot/internal/config"
	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"
)

const shutdownTimeout = 10 * time.Second

type SubagentToolset struct {
	runner *subagentRunner
}

func NewSubagentToolset(agent *Agent, skills *tools.SkillLoader, cfg *config.Config, rt runtime.Runtime, taskManager *tasks.Manager) *SubagentToolset {
	return &SubagentToolset{runner: newSubagentRunner(agent, skills, cfg, rt, taskManager)}
}

type FleetToolset struct {
	runner *subagentRunner
}

func NewFleetToolset(agent *Agent, skills *tools.SkillLoader, cfg *config.Config, rt runtime.Runtime, taskManager *tasks.Manager) *FleetToolset {
	return &FleetToolset{runner: newSubagentRunner(agent, skills, cfg, rt, taskManager)}
}

type subagentRunner struct {
	agent       *Agent
	skills      *tools.SkillLoader
	cfg         *config.Config
	rt          runtime.Runtime
	taskManager *tasks.Manager
}

func newSubagentRunner(agent *Agent, skills *tools.SkillLoader, cfg *config.Config, rt runtime.Runtime, taskManager *tasks.Manager) *subagentRunner {
	return &subagentRunner{agent: agent, skills: skills, cfg: cfg, rt: rt, taskManager: taskManager}
}

func taskDescription(systemPrompt, task string) string {
	if systemPrompt == "" {
		return task
	}
	if task == "" {
		return systemPrompt
	}
	return fmt.Sprintf("system_prompt:\n%s\n\ntask:\n%s", systemPrompt, task)
}

func (r *subagentRunner) startAgentTask(ctx context.Context, systemPrompt, task string) (tasks.Snapshot, error) {
	if r.taskManager == nil {
		return tasks.Snapshot{}, fmt.Errorf("task manager is nil")
	}
	cfg := *r.cfg
	driver := tasks.FuncDriver(func(taskCtx context.Context, info tasks.TaskInfo, emit *tasks.Emitter) (tasks.Controller, error) {
		input := make(chan string, 32)
		innerCtx, cancel := context.WithCancel(taskCtx)
		ctrl := newAgentTaskController(input, cancel)
		go func() {
			defer cancel()
			taskManager := tasks.NewManager(cfg.Tool.MaxOutputChars)
			cmdTools := tools.NewCommandToolset(r.rt, taskManager)
			defer func() {
				ctx2, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
				defer cancel()
				_ = taskManager.Shutdown(ctx2)
			}()
			reg := NewSubagentRegistry(r.rt, r.skills, &cfg, cmdTools)
			orch := NewSubagentOrchestrator(reg, emit, input)
			loop := NewAgentLoop(&cfg, r.agent)
			err := loop.Run(innerCtx, nil, reg, orch, NewSubagentPrompt(r.skills, r.rt, systemPrompt), task)
			if err != nil {
				emit.Output(err.Error())
				emit.Complete(tasks.TaskResult{Error: err.Error()})
				return
			}
			emit.Complete(tasks.TaskResult{})
		}()
		return ctrl, nil
	})
	return r.taskManager.Start(ctx, tasks.StartOptions{
		Description: taskDescription(systemPrompt, task),
		Driver:      driver,
	})
}

func (r *subagentRunner) startFleetTask(ctx context.Context, systemPrompt string, taskList []string) ([]string, error) {
	if r.taskManager == nil {
		return nil, fmt.Errorf("task manager is nil")
	}
	childIDs := make([]string, 0, len(taskList))
	for _, item := range taskList {
		child, err := r.startAgentTask(ctx, systemPrompt, item)
		if err != nil {
			for _, childID := range childIDs {
				_, _ = r.taskManager.Kill(context.Background(), childID)
				_ = r.taskManager.Remove(context.Background(), childID)
			}
			return nil, err
		}
		childIDs = append(childIDs, child.TaskID)
	}
	return childIDs, nil
}

func (s *SubagentToolset) Register(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "agent",
		Description: "Start an isolated asynchronous agent task and return its `task_id` immediately.\n\nThe subagent has access to the same tools and runs with a fresh conversation.\nIt cannot spawn further subagents.\nUse this to delegate well-defined, isolated work units that can be\ncompleted independently of the current conversation context.",
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
			return tools.ToolResult{}, fmt.Errorf("parse agent args: %w", err)
		}
		slog.Debug("subagent start", "task_len", len(p.Task))
		snap, err := s.runner.startAgentTask(ctx, p.SystemPrompt, p.Task)
		if err != nil {
			slog.Debug("subagent error", "err", err)
			return tools.ToolResult{}, err
		}
		return tools.TextResult(tools.MarshalResult(map[string]any{"task_id": snap.TaskID})), nil
	})
}

func (s *FleetToolset) Register(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "fleet",
		Description: "Start multiple asynchronous agent tasks and return all launched `task_id`s immediately.\n\nUse this to fan out well-defined, independent work units that can be\nexecuted concurrently.",
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
			return tools.ToolResult{}, fmt.Errorf("parse fleet args: %w", err)
		}
		slog.Debug("fleet start", "tasks", len(p.Tasks))
		taskIDs, err := s.runner.startFleetTask(ctx, p.SystemPrompt, p.Tasks)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return tools.TextResult(tools.MarshalResult(map[string]any{"task_ids": taskIDs})), nil
	})
}

type agentTaskController struct {
	input  chan string
	cancel context.CancelFunc
}

func newAgentTaskController(input chan string, cancel context.CancelFunc) *agentTaskController {
	return &agentTaskController{input: input, cancel: cancel}
}

func (c *agentTaskController) WriteInput(input string) error {
	select {
	case c.input <- input:
		return nil
	default:
		return fmt.Errorf("task input buffer is full")
	}
}

func (c *agentTaskController) Kill() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
