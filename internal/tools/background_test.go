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

func newCommandTestToolset() (*CommandToolset, *tasks.Manager, *Registry) {
	rt := runtime.NewHostRuntime(4096)
	manager := tasks.NewManager(4096)
	toolset := NewCommandToolset(rt, manager)
	reg := NewRegistry()
	toolset.Register(reg)
	return toolset, manager, reg
}

func TestRunCommandReturnsTaskID(t *testing.T) {
	_, manager, reg := newCommandTestToolset()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	handler, ok := reg.Handler("run_command")
	if !ok {
		t.Fatal("expected run_command handler")
	}
	result, err := handler(context.Background(), []byte(`{"command":"echo hi", "timeout": 0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload["task_id"] == "" {
		t.Fatalf("expected task_id, got %s", result.Text)
	}
}

func TestRunCommandReturnsFinalResultWithinTimeout(t *testing.T) {
	_, manager, reg := newCommandTestToolset()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	handler, ok := reg.Handler("run_command")
	if !ok {
		t.Fatal("expected run_command handler")
	}
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
	_, manager, reg := newCommandTestToolset()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	handler, ok := reg.Handler("run_command")
	if !ok {
		t.Fatal("expected run_command handler")
	}
	result, err := handler(context.Background(), []byte(`{"command":"sleep 2; echo hi","timeout":1}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload["task_id"] == "" {
		t.Fatalf("expected task_id after timeout fallback, got %s", result.Text)
	}
}

func TestAwaitTaskReportsExitCodeAndOutput(t *testing.T) {
	_, manager, reg := newCommandTestToolset()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	run, _ := reg.Handler("run_command")
	await, _ := reg.Handler("await_task")
	start, err := run(context.Background(), []byte(`{"command":"echo hello && exit 7", "timeout": 0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(start.Text), &payload); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	got, err := await(context.Background(), []byte(`{"task_id":"`+payload["task_id"]+`","timeout":2}`))
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
	_, manager, reg := newCommandTestToolset()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()
	run, _ := reg.Handler("run_command")
	writeInput, _ := reg.Handler("write_to_task")
	await, _ := reg.Handler("await_task")
	start, err := run(context.Background(), []byte(`{"command":"sleep 0.2; read line; echo got:$line", "timeout": 0}`))
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(start.Text), &payload); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	if _, err := writeInput(context.Background(), []byte(`{"task_id":"`+payload["task_id"]+`","input":"hello\n"}`)); err != nil {
		t.Fatalf("write_to_task: %v", err)
	}
	got, err := await(context.Background(), []byte(`{"task_id":"`+payload["task_id"]+`","timeout":2}`))
	if err != nil {
		t.Fatalf("await_task: %v", err)
	}
	if !strings.Contains(got.Text, "output:\ngot:hello") {
		t.Fatalf("expected stdin to reach process, got:\n%s", got.Text)
	}
}
