package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"my-bot/internal/runtime"
)

const killGracePeriod = 2 * time.Second

type OutputStream struct {
	mu        sync.Mutex
	buf       []byte
	capacity  int
	truncated bool
}

func newOutputStream(capacity int) *OutputStream {
	return &OutputStream{capacity: capacity}
}

func (s *OutputStream) Append(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, data...)
	if len(s.buf) > s.capacity {
		s.buf = s.buf[len(s.buf)-s.capacity:]
		s.truncated = true
	}
}

func (s *OutputStream) Render() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.truncated {
		return fmt.Sprintf("[output truncated; showing the last %d bytes]\n\n%s", len(s.buf), string(s.buf))
	}
	return string(s.buf)
}

type taskStatus string

const (
	taskRunning taskStatus = "running"
	taskExited  taskStatus = "exited"
	taskKilled  taskStatus = "killed"
)

func (s taskStatus) String() string { return string(s) }

type TaskSnapshot struct {
	TaskID         string     `json:"task_id"`
	Command        string     `json:"command"`
	Stdout         string     `json:"stdout,omitempty"`
	Stderr         string     `json:"stderr,omitempty"`
	Status         taskStatus `json:"status"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	ElapsedSeconds *float64   `json:"elapsed_seconds,omitempty"`
}

type BackgroundTask struct {
	TaskID    string
	Command   string
	PID       int
	startMono time.Time
	proc      *runtime.ProcessHandle
	stdout    *OutputStream
	stderr    *OutputStream
	done      chan struct{}
	killed    atomic.Bool
}

func newBackgroundTask(id, command string, proc *runtime.ProcessHandle, maxChars int) *BackgroundTask {
	t := &BackgroundTask{
		TaskID:    id,
		Command:   command,
		PID:       proc.PID,
		startMono: time.Now(),
		proc:      proc,
		stdout:    newOutputStream(maxChars),
		stderr:    newOutputStream(maxChars),
		done:      make(chan struct{}),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); t.pump(proc.Stdout, t.stdout) }()
	go func() { defer wg.Done(); t.pump(proc.Stderr, t.stderr) }()
	go func() {
		wg.Wait()
		_ = proc.Wait()
		close(t.done)
	}()
	return t
}

func (t *BackgroundTask) pump(r io.Reader, out *OutputStream) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out.Append(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (t *BackgroundTask) status() taskStatus {
	select {
	case <-t.done:
		if t.killed.Load() {
			return taskKilled
		}
		return taskExited
	default:
		return taskRunning
	}
}

func (t *BackgroundTask) snapshot(includeOutput bool) TaskSnapshot {
	status := t.status()
	snap := TaskSnapshot{
		TaskID:  t.TaskID,
		Command: t.Command,
		Status:  status,
	}
	if includeOutput {
		snap.Stdout = t.stdout.Render()
		snap.Stderr = t.stderr.Render()
	}
	if exitCode := t.proc.ExitCode(); exitCode != nil && *exitCode != 0 {
		snap.ExitCode = exitCode
	}
	if status == taskRunning {
		elapsed := time.Since(t.startMono).Seconds()
		snap.ElapsedSeconds = &elapsed
	}
	return snap
}

func (t *BackgroundTask) Wait(ctx context.Context, timeout time.Duration) TaskSnapshot {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return t.snapshot(true)
}

func (t *BackgroundTask) Kill(ctx context.Context) error {
	t.killed.Store(true)
	_ = t.proc.Terminate()
	select {
	case <-t.done:
		return nil
	case <-time.After(killGracePeriod):
		_ = t.proc.Kill()
		select {
		case <-t.done:
		case <-time.After(time.Second):
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type TaskMonitor struct {
	rt             runtime.Runtime
	maxOutputChars int
	tasks          map[string]*BackgroundTask
	nextID         int
}

func NewTaskMonitor(rt runtime.Runtime, maxOutputChars int) *TaskMonitor {
	return &TaskMonitor{
		rt:             rt,
		maxOutputChars: maxOutputChars,
		tasks:          make(map[string]*BackgroundTask),
	}
}

func (m *TaskMonitor) nextTaskID() string {
	m.nextID++
	return fmt.Sprintf("task-%d", m.nextID)
}

func (m *TaskMonitor) Spawn(ctx context.Context, command string) (*BackgroundTask, error) {
	proc, err := m.rt.Spawn(ctx, command)
	if err != nil {
		return nil, err
	}
	id := m.nextTaskID()
	t := newBackgroundTask(id, command, proc, m.maxOutputChars)
	m.tasks[id] = t
	return t, nil
}

func (m *TaskMonitor) Get(taskID string) (*BackgroundTask, error) {
	t, ok := m.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %q not found", taskID)
	}
	return t, nil
}

func (m *TaskMonitor) List() []TaskSnapshot {
	out := make([]TaskSnapshot, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.snapshot(false))
	}
	return out
}

func (m *TaskMonitor) KillTask(ctx context.Context, taskID string) (TaskSnapshot, error) {
	t, err := m.Get(taskID)
	if err != nil {
		return TaskSnapshot{}, err
	}
	if err := t.Kill(ctx); err != nil {
		return TaskSnapshot{}, err
	}
	return t.snapshot(true), nil
}

func (m *TaskMonitor) RemoveTask(ctx context.Context, taskID string) error {
	t, err := m.Get(taskID)
	if err != nil {
		return err
	}
	if t.status() == taskRunning {
		if err := t.Kill(ctx); err != nil {
			return err
		}
	}
	delete(m.tasks, taskID)
	return nil
}

func (m *TaskMonitor) Shutdown(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, t := range m.tasks {
		wg.Add(1)
		go func(t *BackgroundTask) { defer wg.Done(); _ = t.Kill(ctx) }(t)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return nil
}

type CommandToolset struct {
	monitor *TaskMonitor
}

func NewCommandToolset(rt runtime.Runtime, maxOutputChars int) *CommandToolset {
	return &CommandToolset{monitor: NewTaskMonitor(rt, maxOutputChars)}
}

func (s *CommandToolset) Shutdown(ctx context.Context) error {
	return s.monitor.Shutdown(ctx)
}

func (s *CommandToolset) Register(r *Registry) {
	r.Register(ToolSchema{
		Name:        "run_command",
		Description: "Run a shell command and wait up to `timeout_seconds` for it to finish.\n\nUse this for operations with no dedicated tool: scripts, package installs, tests, build tools, git, etc.\n\nIf timeout expires, the process is promoted to a background task (not killed). Use await_task(task_id, ...) to keep waiting, or kill_task(task_id). Prefer run_command_background() when you already know the command is long-running.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute. Use dedicated tools instead of shell equivalents: read_file not cat, grep not grep/rg, glob not find/ls, edit_file not sed/awk.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"default":     60,
					"description": "Maximum seconds to wait synchronously (default: 60, maximum: 600). If the command is still running, it is promoted to a background task and a task_id is returned.",
				},
			},
			"required": []string{"command"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Command        string `json:"command"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		p.TimeoutSeconds = 60
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		if p.TimeoutSeconds > 600 {
			p.TimeoutSeconds = 600
		}
		t, err := s.monitor.Spawn(ctx, p.Command)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(formatRunCommandResult(t.Wait(ctx, time.Duration(p.TimeoutSeconds)*time.Second))), nil
	})

	r.Register(ToolSchema{
		Name:        "run_command_background",
		Description: "Start a shell command and return immediately without waiting for it.\n\nUse when you know the command is long-running and waiting is wrong (dev servers, watchers).",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to start. Returns immediately with a task_id; use await_task to fetch output or wait for exit.",
				},
			},
			"required": []string{"command"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		t, err := s.monitor.Spawn(ctx, p.Command)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(MarshalResult(map[string]any{"task_id": t.TaskID})), nil
	})

	r.Register(ToolSchema{
		Name:        "await_task",
		Description: "Wait for a background task to exit.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task identifier returned by run_command_background or by run_command when it was promoted on timeout.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"default":     60,
					"description": "Maximum seconds to wait this call (default: 60, maximum: 600). Returns early if the task exits.",
				},
			},
			"required": []string{"task_id"},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			TaskID         string `json:"task_id"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		p.TimeoutSeconds = 60
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, err
		}
		if p.TimeoutSeconds > 600 {
			p.TimeoutSeconds = 600
		}
		t, err := s.monitor.Get(p.TaskID)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(formatTaskSnapshot(t.Wait(ctx, time.Duration(p.TimeoutSeconds)*time.Second))), nil
	})

	r.Register(ToolSchema{
		Name:        "kill_task",
		Description: "Terminate a background task (SIGTERM, then SIGKILL after a 2-second grace).",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task identifier from list_tasks / run_command_background.",
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
		snap, err := s.monitor.KillTask(ctx, p.TaskID)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(formatTaskSnapshot(snap)), nil
	})

	r.Register(ToolSchema{
		Name:        "list_tasks",
		Description: "List background tasks started in this chat session.",
		ParameterDesc: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		return TextResult(MarshalResult(s.monitor.List())), nil
	})

	r.Register(ToolSchema{
		Name:        "remove_task",
		Description: "Remove a task from the monitor. If the task is still running it is killed first.",
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
		if err := s.monitor.RemoveTask(ctx, p.TaskID); err != nil {
			return ToolResult{}, err
		}
		return TextResult(MarshalResult(map[string]any{"removed": p.TaskID})), nil
	})
}
