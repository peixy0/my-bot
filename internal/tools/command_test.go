package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
)

func newCommandTestToolset(t *testing.T) (*CommandToolset, *tasks.Manager, *Registry) {
	t.Helper()
	rt := runtime.NewHostRuntime(4096)
	manager := tasks.NewManager(rt, 4096)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	toolset := NewCommandToolset(rt, manager)
	reg := NewRegistry()
	toolset.Register(reg)
	return toolset, manager, reg
}

type preparedExecutor func(context.Context, []byte) (ToolResult, error)

func mustPreparedExecutor(t *testing.T, reg *Registry, name string) preparedExecutor {
	t.Helper()
	return func(ctx context.Context, args []byte) (ToolResult, error) {
		preparer, ok := reg.Get(name)
		if !ok {
			t.Fatalf("expected preparer %q to be found", name)
		}
		prepared, err := preparer(args)
		if err != nil {
			return ToolResult{}, err
		}
		if prepared.Description == "" {
			t.Fatalf("expected description for %q", name)
		}
		return prepared.Execute(ctx)
	}
}

func parseTaskID(t *testing.T, result ToolResult) string {
	t.Helper()
	var payload map[string]string
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("parse task_id: %v", err)
	}
	if payload["task_id"] == "" {
		t.Fatal("expected non-empty task_id")
	}
	return payload["task_id"]
}

func TestCommandPreparerRejectsInvalidArguments(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	preparer, ok := reg.Get("run_command")
	if !ok {
		t.Fatal("run_command was not registered")
	}
	for _, args := range [][]byte{[]byte(`{`), []byte(`{}`), []byte(`{"command":""}`)} {
		if _, err := preparer(args); err == nil {
			t.Fatalf("expected preparation error for %s", args)
		}
	}
}

func TestRunCommandReturnsTaskID(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	handler := mustPreparedExecutor(t, reg, "run_command")
	result, err := handler(context.Background(), []byte(`{"command":"echo hi", "timeout": 0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	parseTaskID(t, result)
}

func TestRunCommandReturnsFinalResultWithinTimeout(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	handler := mustPreparedExecutor(t, reg, "run_command")
	result, err := handler(context.Background(), []byte(`{"command":"echo hi","timeout":2}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	if !strings.Contains(result.Text, "status: exited") {
		t.Fatalf("expected final snapshot, got:\n%s", result.Text)
	}
	if !strings.Contains(result.Text, "output:\nhi") {
		t.Fatalf("expected command output, got:\n%s", result.Text)
	}
}

func TestRunCommandReturnsTaskIDAfterTimeout(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	handler := mustPreparedExecutor(t, reg, "run_command")
	result, err := handler(context.Background(), []byte(`{"command":"sleep 2; echo hi","timeout":1}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	parseTaskID(t, result)
}

func TestAwaitTaskReportsExitCodeAndOutput(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	run := mustPreparedExecutor(t, reg, "run_command")
	await := mustPreparedExecutor(t, reg, "await_task")
	start, err := run(context.Background(), []byte(`{"command":"echo hello && exit 7", "timeout": 0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	taskID := parseTaskID(t, start)
	got, err := await(context.Background(), []byte(`{"task_id":"`+taskID+`","timeout":2}`))
	if err != nil {
		t.Fatalf("await_task: %v", err)
	}
	if !strings.Contains(got.Text, "status: exited") {
		t.Fatalf("expected exited status, got:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "output:\nhello") {
		t.Fatalf("expected output, got:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "exit_code: 7") {
		t.Fatalf("expected output, got:\n%s", got.Text)
	}
}

func TestWriteTaskInputFeedsProcessStdin(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	run := mustPreparedExecutor(t, reg, "run_command")
	writeInput := mustPreparedExecutor(t, reg, "write_to_task")
	await := mustPreparedExecutor(t, reg, "await_task")
	start, err := run(context.Background(), []byte(`{"command":"sleep 0.2; read line; echo got:$line", "timeout": 0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	taskID := parseTaskID(t, start)
	if _, err := writeInput(context.Background(), []byte(`{"task_id":"`+taskID+`","input":"hello\n"}`)); err != nil {
		t.Fatalf("write_to_task: %v", err)
	}
	got, err := await(context.Background(), []byte(`{"task_id":"`+taskID+`","timeout":2}`))
	if err != nil {
		t.Fatalf("await_task: %v", err)
	}
	if !strings.Contains(got.Text, "output:\nhello\ngot:hello") {
		t.Fatalf("expected stdin to reach process, got:\n%s", got.Text)
	}
}

func TestGetTaskReturnsSnapshot(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	run := mustPreparedExecutor(t, reg, "run_command")
	get := mustPreparedExecutor(t, reg, "get_task")
	start, err := run(context.Background(), []byte(`{"command":"sleep 1; echo hi","timeout":0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	taskID := parseTaskID(t, start)
	got, err := get(context.Background(), []byte(`{"task_id":"`+taskID+`"}`))
	if err != nil {
		t.Fatalf("get_task: %v", err)
	}
	if !strings.Contains(got.Text, "task_id: "+taskID) {
		t.Fatalf("expected snapshot for %s, got:\n%s", taskID, got.Text)
	}
	if !strings.Contains(got.Text, "status: ") {
		t.Fatalf("expected a status field, got:\n%s", got.Text)
	}
}

func TestListTasksReturnsNonEmpty(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	run := mustPreparedExecutor(t, reg, "run_command")
	list := mustPreparedExecutor(t, reg, "list_tasks")
	start, err := run(context.Background(), []byte(`{"command":"sleep 10","timeout":0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	taskID := parseTaskID(t, start)
	got, err := list(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("list_tasks: %v", err)
	}
	if got.Text == "" {
		t.Fatal("expected non-empty list_tasks result")
	}
	if !strings.Contains(got.Text, taskID) {
		t.Fatalf("expected list to contain %s, got:\n%s", taskID, got.Text)
	}
}

func TestKillTaskTerminatesRunningTask(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	run := mustPreparedExecutor(t, reg, "run_command")
	kill := mustPreparedExecutor(t, reg, "kill_task")
	get := mustPreparedExecutor(t, reg, "get_task")
	start, err := run(context.Background(), []byte(`{"command":"sleep 10","timeout":0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	taskID := parseTaskID(t, start)
	got, err := kill(context.Background(), []byte(`{"task_id":"`+taskID+`"}`))
	if err != nil {
		t.Fatalf("kill_task: %v", err)
	}
	if !strings.Contains(got.Text, `"status":"killed"`) {
		t.Fatalf("expected killed status, got:\n%s", got.Text)
	}
	after, err := get(context.Background(), []byte(`{"task_id":"`+taskID+`"}`))
	if err != nil {
		t.Fatalf("get_task: %v", err)
	}
	if !strings.Contains(after.Text, "error:") {
		t.Fatalf("expected error result after killing removed task, got:\n%s", after.Text)
	}
}

func TestKillTaskNonExistentReturnsError(t *testing.T) {
	_, _, reg := newCommandTestToolset(t)
	kill := mustPreparedExecutor(t, reg, "kill_task")
	got, err := kill(context.Background(), []byte(`{"task_id":"does-not-exist"}`))
	if err != nil {
		t.Fatalf("kill_task: %v", err)
	}
	if !strings.Contains(got.Text, "error:") {
		t.Fatalf("expected error result for non-existent task, got:\n%s", got.Text)
	}
}
