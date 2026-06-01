package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"my-bot/internal/runtime"
)

func TestRunCommand_DefaultTimeout60(t *testing.T) {
	type params struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	p := params{TimeoutSeconds: 60}
	if err := json.Unmarshal([]byte(`{"command":"echo hi"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.TimeoutSeconds != 60 {
		t.Errorf("expected default timeout 60, got %d", p.TimeoutSeconds)
	}
}

func TestOutputStream_TruncationBehavior(t *testing.T) {
	s := newOutputStream(10)

	s.Append([]byte("hello"))
	if s.Render() != "hello" {
		t.Errorf("expected 'hello', got %q", s.Render())
	}

	s.Append([]byte(" world!!!"))
	rendered := s.Render()
	if len(rendered) == 0 {
		t.Fatal("expected non-empty output")
	}
	if rendered[:len("[output truncated; showing the last 10 bytes]")] != "[output truncated; showing the last 10 bytes]" {
		t.Errorf("expected truncation prefix, got %q", rendered[:30])
	}
}

func TestTaskMonitor_SpawnAndList(t *testing.T) {
	m := &TaskMonitor{
		tasks:          make(map[string]*BackgroundTask),
		maxOutputChars: 1000,
	}
	id1 := m.nextTaskID()
	id2 := m.nextTaskID()
	if id1 == id2 {
		t.Errorf("expected unique IDs, got %q and %q", id1, id2)
	}
	if id1 != "task-1" || id2 != "task-2" {
		t.Errorf("expected task-1, task-2, got %q, %q", id1, id2)
	}
}

func TestBackgroundTask_SetsExitCodeAfterExit(t *testing.T) {
	rt := runtime.NewHostRuntime(1024)
	proc, err := rt.Spawn(context.Background(), "exit 7")
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	task := newBackgroundTask("task-1", "exit 7", proc, 1024)
	snap := task.Wait(context.Background(), 2*time.Second)
	if snap.Status != taskExited {
		t.Fatalf("expected task status exited, got %s", snap.Status.String())
	}
	if snap.ExitCode == nil {
		t.Fatal("expected non-nil exit code")
	}
	if *snap.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", *snap.ExitCode)
	}
}
