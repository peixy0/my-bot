package tasks

import (
	"context"
	"testing"
	"time"
)

func TestStartDetachesTaskLifetimeFromCallerContext(t *testing.T) {
	manager := NewManager(1024)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	driver := FuncDriver(func(taskCtx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		go func() {
			started <- struct{}{}
			<-time.After(50 * time.Millisecond)
			select {
			case <-taskCtx.Done():
				emit.Complete(TaskResult{Error: taskCtx.Err().Error()})
			default:
				emit.Output("finished")
				emit.Complete(TaskResult{})
			}
		}()
		return nil, nil
	})

	snap, err := manager.Start(ctx, StartOptions{Description: "test", Driver: driver})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-started
	cancel()

	got, err := manager.Await(context.Background(), snap.TaskID, time.Second)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if got.Status != StatusExited {
		t.Fatalf("expected task to keep running after caller cancel, got status=%s output=%q error=%q", got.Status, got.Output, got.Error)
	}
	if got.Error != "" {
		t.Fatalf("expected no task error after caller cancel, got %q", got.Error)
	}
}
