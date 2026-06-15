package tasks

import (
	"context"
	"fmt"
	"io"
	"sync"

	"my-bot/internal/runtime"
)

type FuncDriver func(context.Context, TaskInfo, *Emitter) (Controller, error)

func (d FuncDriver) Start(ctx context.Context, info TaskInfo, emit *Emitter) (Controller, error) {
	return d(ctx, info, emit)
}

func NewProcessDriver(rt runtime.Runtime, command string) Driver {
	return FuncDriver(func(ctx context.Context, _ TaskInfo, emit *Emitter) (Controller, error) {
		proc, err := rt.Spawn(ctx, command)
		if err != nil {
			return nil, err
		}
		ctrl := &processController{proc: proc}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			pumpStream(proc.Stdout, emit.Output)
		}()
		go func() {
			defer wg.Done()
			pumpStream(proc.Stderr, emit.Output)
		}()
		go func() {
			err := proc.Wait()
			wg.Wait()
			if code := proc.ExitCode(); code != nil {
				emit.Output(fmt.Sprintf("exit_code: %d\n", *code))
				emit.Complete(TaskResult{})
				return
			}
			if err != nil {
				emit.Complete(TaskResult{Error: err.Error()})
				return
			}
			emit.Complete(TaskResult{})
		}()
		return ctrl, nil
	})
}

type processController struct {
	proc *runtime.ProcessHandle
}

func (c *processController) WriteInput(input string) error {
	if c.proc == nil || c.proc.Stdin == nil {
		return ErrInputUnsupported
	}
	_, err := io.WriteString(c.proc.Stdin, input)
	return err
}

func (c *processController) Kill() error {
	if c.proc == nil {
		return nil
	}
	return c.proc.Kill()
}

func pumpStream(r io.Reader, fn func(string)) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			fn(string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}
