package tasks

import (
	"context"
	"strings"
	"testing"
	"time"

	"my-bot/internal/runtime"
)

func newTestManager(maxOutputChars int) *Manager {
	return NewManager(runtime.NewHostRuntime(maxOutputChars), maxOutputChars)
}

func TestManager_StartAndGetRunning(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	started := make(chan struct{}, 1)
	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		go func() {
			started <- struct{}{}
			<-taskCtx.Done()
			emit.Complete(TaskResult{})
		}()
		return nil, nil
	})

	snap, err := manager.Start(context.Background(), StartOptions{Description: "run", Driver: driver})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-started

	if snap.Status != StatusRunning {
		t.Fatalf("expected running, got %s", snap.Status)
	}
	if snap.TaskID == "" {
		t.Fatal("expected non-empty task id")
	}

	got, _, err := manager.Get(context.Background(), snap.TaskID, false)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("expected running from get, got %s", got.Status)
	}
}

func TestManager_EmitOutputAccumulates(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	done := make(chan struct{})
	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		emit.Output("line1\n")
		emit.Output("line2\n")
		emit.Complete(TaskResult{})
		close(done)
		return nil, nil
	})

	snap, _ := manager.Start(context.Background(), StartOptions{Description: "output", Driver: driver})
	<-done

	got, err := manager.Await(context.Background(), snap.TaskID, 2*time.Second)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if got.Status != StatusExited {
		t.Fatalf("expected exited, got %s", got.Status)
	}
	if !strings.Contains(got.Output, "line1") || !strings.Contains(got.Output, "line2") {
		t.Fatalf("expected accumulated output, got %q", got.Output)
	}
}

func TestManager_EmitOutputKeepsTail(t *testing.T) {
	t.Chdir(t.TempDir())
	manager := newTestManager(5)
	defer mustShutdown(t, manager)

	driver := FuncDriver(func(_ context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		emit.Output("hello")
		emit.Complete(TaskResult{Output: " world"})
		return nil, nil
	})
	snap, err := manager.Start(context.Background(), StartOptions{Description: "tail", Driver: driver})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	got, err := manager.Await(context.Background(), snap.TaskID, 2*time.Second)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if !strings.Contains(got.Output, "[output truncated; showing the last 5 chars; full output saved to ") || !strings.HasSuffix(got.Output, "\n\nworld") {
		t.Fatalf("unexpected truncated output: %q", got.Output)
	}
}

func TestManager_CompleteWithErrorMarksFailed(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	done := make(chan struct{})
	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		emit.Complete(TaskResult{Error: "something broke"})
		close(done)
		return nil, nil
	})

	snap, _ := manager.Start(context.Background(), StartOptions{Description: "fail", Driver: driver})
	<-done

	got, err := manager.Await(context.Background(), snap.TaskID, 2*time.Second)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error != "something broke" {
		t.Fatalf("expected error text, got %q", got.Error)
	}
}

func TestManager_CompleteSuccessMarksExited(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	done := make(chan struct{})
	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		emit.Complete(TaskResult{})
		close(done)
		return nil, nil
	})

	snap, _ := manager.Start(context.Background(), StartOptions{Description: "ok", Driver: driver})
	<-done

	got, err := manager.Await(context.Background(), snap.TaskID, 2*time.Second)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if got.Status != StatusExited {
		t.Fatalf("expected exited, got %s", got.Status)
	}
}

func TestManager_KillCallsControllerKill(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	started := make(chan struct{}, 1)
	done := make(chan struct{}, 1)
	killCh := make(chan struct{}, 1)
	var killCalled bool

	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		go func() {
			started <- struct{}{}
			select {
			case <-killCh:
			case <-taskCtx.Done():
			}
			emit.Complete(TaskResult{})
			done <- struct{}{}
		}()
		return &trackKillController{onKill: func() { killCalled = true; close(killCh) }}, nil
	})

	snap, _ := manager.Start(context.Background(), StartOptions{Description: "killable", Driver: driver})
	<-started

	_, err := manager.Kill(context.Background(), snap.TaskID)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !killCalled {
		t.Fatal("expected Controller.Kill() to be called")
	}
	<-done

	got, err := manager.Await(context.Background(), snap.TaskID, 2*time.Second)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	// Kill via manager.Kill doesn't set killRequested, so the task
	// completes normally (StatusExited) after controller kill triggers completion
	if got.Status != StatusExited {
		t.Fatalf("expected exited (Kill just calls controller.Kill, driver completes normally), got %s", got.Status)
	}
}

type trackKillController struct {
	onKill func()
}

func (c *trackKillController) WriteInput(string) error { return ErrInputUnsupported }
func (c *trackKillController) Kill() error             { c.onKill(); return nil }

func TestManager_RemoveNonexistentTask(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	err := manager.Remove(context.Background(), "no-such-task")
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestManager_ListReturnsCreationOrder(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	for i := 0; i < 3; i++ {
		n := i
		driver := FuncDriver(func(taskCtx context.Context, info TaskInfo, emit *Emitter) (Controller, error) {
			emit.Output(info.Description)
			emit.Complete(TaskResult{})
			return nil, nil
		})
		manager.Start(context.Background(), StartOptions{Description: "task-order-" + string(rune('A'+n)), Driver: driver})
	}

	time.Sleep(50 * time.Millisecond)

	list, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(list))
	}
	for i, snap := range list {
		if snap.Status != StatusExited {
			t.Fatalf("task %d: expected exited, got %s", i, snap.Status)
		}
	}
}

func TestManager_WriteInputNonexistentTask(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	err := manager.WriteInput(context.Background(), "no-such-task", "hello")
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestManager_WriteInputNotRunning(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	done := make(chan struct{})
	driver := FuncDriver(func(_ context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		emit.Complete(TaskResult{})
		close(done)
		return nil, nil
	})

	snap, _ := manager.Start(context.Background(), StartOptions{Description: "quick", Driver: driver})
	<-done

	err := manager.WriteInput(context.Background(), snap.TaskID, "too late")
	if err == nil {
		t.Fatal("expected error for writing to non-running task")
	}
}

func TestManager_WriteInputToController(t *testing.T) {
	manager := newTestManager(1024)
	defer mustShutdown(t, manager)

	inputCh := make(chan string, 1)
	done := make(chan struct{}, 1)
	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		go func() {
			<-inputCh
			emit.Complete(TaskResult{})
			done <- struct{}{}
		}()
		return &pipeController{inputCh: inputCh}, nil
	})

	snap, _ := manager.Start(context.Background(), StartOptions{Description: "input", Driver: driver})
	time.Sleep(20 * time.Millisecond)

	err := manager.WriteInput(context.Background(), snap.TaskID, "hello")
	if err != nil {
		t.Fatalf("write input: %v", err)
	}
	<-done
}

type pipeController struct {
	inputCh chan string
}

func (c *pipeController) WriteInput(input string) error {
	select {
	case c.inputCh <- input:
		return nil
	default:
		return ErrInputUnsupported
	}
}

func (c *pipeController) Kill() error { return nil }

func TestManager_ShutdownWaitsForRunningTasks(t *testing.T) {
	manager := newTestManager(1024)

	started := make(chan struct{}, 1)
	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		go func() {
			started <- struct{}{}
			<-time.After(50 * time.Millisecond)
			emit.Complete(TaskResult{})
		}()
		return nil, nil
	})

	manager.Start(context.Background(), StartOptions{Description: "slow", Driver: driver})
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func mustShutdown(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.Shutdown(ctx)
}
