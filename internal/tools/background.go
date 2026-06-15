package tools

import (
	"context"
	"encoding/json"
	"time"

	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
)

type CommandToolset struct {
	rt    runtime.Runtime
	tasks *tasks.Manager
}

func NewCommandToolset(rt runtime.Runtime, taskSet *tasks.Manager) *CommandToolset {
	return &CommandToolset{rt: rt, tasks: taskSet}
}

func (s *CommandToolset) Register(r *Registry) {
	r.Register(ToolSchema{
		Name:        "run_command",
		Description: "Start a shell command as a task. By default it returns a `task_id` immediately, but if `timeout` is provided it waits up to that many seconds for the command to finish and returns the final task result when available.\n\nUse this for operations with no dedicated tool: scripts, package installs, tests, build tools, git, etc. Inspect progress with get_task()/await_task(), send input with write_to_task(), and stop it with kill_task().",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute. Use dedicated tools instead of shell equivalents: read_file not cat, grep not grep/rg, glob not find/ls, edit_file not sed/awk.",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"default":     60,
					"description": "Optional: wait up to this many seconds for the command to finish before falling back to returning `task_id`. Default 60, maximum 600. Setting timeout to 0 means to return `task_id` immediately and letting it run in the background without waiting at all.",
				},
			},
			"required": []string{"command"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Command        string `json:"command"`
			TimeoutSeconds int    `json:"timeout"`
		}
		p.TimeoutSeconds = 60
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		snap, err := s.tasks.Start(ctx, tasks.StartOptions{
			Description: p.Command,
			Driver:      tasks.NewProcessDriver(s.rt, p.Command),
		})
		if err != nil {
			return ToolResult{}, err
		}
		if p.TimeoutSeconds > 0 {
			if p.TimeoutSeconds > 600 {
				p.TimeoutSeconds = 600
			}
			got, err := s.tasks.Await(ctx, snap.TaskID, time.Duration(p.TimeoutSeconds)*time.Second)
			if err != nil {
				return ToolResult{}, err
			}
			if got.Status != tasks.StatusRunning {
				return TextResult(formatTaskSnapshot(got)), nil
			}
		}
		return TextResult(MarshalResult(map[string]any{"task_id": snap.TaskID})), nil
	})

	r.Register(ToolSchema{
		Name:        "get_task",
		Description: "Get the current snapshot of a task, including captured output so far.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task identifier to inspect.",
				},
			},
			"required": []string{"task_id"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		snap, _, err := s.tasks.Get(ctx, p.TaskID, true)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(formatTaskSnapshot(snap)), nil
	})

	r.Register(ToolSchema{
		Name:        "await_task",
		Description: "Wait for a task to make progress or exit, then return its latest snapshot.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task identifier returned by run_command(), agent(), or fleet().",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"default":     60,
					"description": "Maximum seconds to wait this call (default: 60, maximum: 600). Returns the current snapshot if the timeout expires first.",
				},
			},
			"required": []string{"task_id"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			TaskID         string `json:"task_id"`
			TimeoutSeconds int    `json:"timeout"`
		}
		p.TimeoutSeconds = 60
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		if p.TimeoutSeconds > 600 {
			p.TimeoutSeconds = 600
		}
		snap, err := s.tasks.Await(ctx, p.TaskID, time.Duration(p.TimeoutSeconds)*time.Second)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(formatTaskSnapshot(snap)), nil
	})

	r.Register(ToolSchema{
		Name:        "kill_task",
		Description: "Kill a running task immediately.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task identifier from list_tasks.",
				},
			},
			"required": []string{"task_id"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		snap, err := s.tasks.Kill(ctx, p.TaskID)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(formatTaskSnapshot(snap)), nil
	})

	r.Register(ToolSchema{
		Name:        "list_tasks",
		Description: "List tasks started in this chat session.",
		ParameterDesc: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		snaps, err := s.tasks.List(ctx)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(MarshalResult(snaps)), nil
	})

	r.Register(ToolSchema{
		Name:        "write_to_task",
		Description: "Send text input to a running task.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task identifier to write input to.",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "Text to send to the task's stdin or inbox.",
				},
			},
			"required": []string{"task_id", "input"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			TaskID string `json:"task_id"`
			Input  string `json:"input"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		if err := s.tasks.WriteInput(ctx, p.TaskID, p.Input); err != nil {
			return ToolResult{}, err
		}
		return TextResult(MarshalResult(map[string]any{"task_id": p.TaskID, "written": len(p.Input)})), nil
	})

	r.Register(ToolSchema{
		Name:        "remove_task",
		Description: "Remove a task from memory. Running tasks are killed first.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task identifier to remove.",
				},
			},
			"required": []string{"task_id"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		if _, err := s.tasks.Kill(ctx, p.TaskID); err != nil {
			var snapErr error
			if _, _, snapErr = s.tasks.Get(ctx, p.TaskID, false); snapErr != nil {
				return ToolResult{}, err
			}
		}
		if err := s.tasks.Remove(ctx, p.TaskID); err != nil {
			return ToolResult{}, err
		}
		return TextResult(MarshalResult(map[string]any{"removed": p.TaskID})), nil
	})
}
