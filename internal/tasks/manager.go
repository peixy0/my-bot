package tasks

import (
	"context"
	"fmt"
	"time"

	"my-bot/internal/runtime"
)

type Manager struct {
	rt             runtime.Runtime
	maxOutputChars int
	rootCtx        context.Context
	rootCancel     context.CancelFunc
	inbox          chan any
	shutdownDone   chan struct{}
}

type startReq struct {
	opts StartOptions
	resp chan startResp
}

type startResp struct {
	snap Snapshot
	err  error
}

type emitReq struct {
	taskID string
	data   string
}

type completeReq struct {
	taskID string
	result TaskResult
}

type getReq struct {
	taskID        string
	includeOutput bool
	resp          chan getResp
}

type getResp struct {
	snap Snapshot
	done doneRef
	err  error
}

type listReq struct {
	resp chan []Snapshot
}

type writeInputReq struct {
	taskID string
	input  string
	resp   chan error
}

type killReq struct {
	taskID string
	resp   chan terminateResp
}

type terminateResp struct {
	snap Snapshot
	err  error
}

type removeReq struct {
	taskID string
	resp   chan error
}

type shutdownReq struct {
	resp chan struct{}
}

type stopReq struct {
	resp chan struct{}
}

type taskState struct {
	info          TaskInfo
	status        Status
	startedAt     time.Time
	completedAt   time.Time
	errText       string
	output        string
	controller    Controller
	ctx           context.Context
	cancel        context.CancelFunc
	stopRootLink  func() bool
	done          chan struct{}
	killRequested bool
}

func NewManager(rt runtime.Runtime, maxOutputChars int) *Manager {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	m := &Manager{
		rt:             rt,
		maxOutputChars: maxOutputChars,
		rootCtx:        rootCtx,
		rootCancel:     rootCancel,
		inbox:          make(chan any, 256),
		shutdownDone:   make(chan struct{}),
	}
	go m.loop()
	return m
}

func (m *Manager) loop() {
	tasks := make(map[string]*taskState)
	order := make([]string, 0, 16)
	nextID := 0
	shuttingDown := false
	for msg := range m.inbox {
		switch req := msg.(type) {
		case startReq:
			nextID++
			taskID := fmt.Sprintf("task-%d", nextID)
			taskCtx, cancel := context.WithCancel(m.rootCtx)
			stopRootLink := context.AfterFunc(m.rootCtx, cancel)
			info := TaskInfo{
				TaskID:      taskID,
				Description: req.opts.Description,
			}
			state := &taskState{
				info:         info,
				status:       StatusRunning,
				startedAt:    time.Now(),
				ctx:          taskCtx,
				cancel:       cancel,
				stopRootLink: stopRootLink,
				done:         make(chan struct{}),
			}
			tasks[taskID] = state
			order = append(order, taskID)
			emit := &Emitter{taskID: taskID, inbox: m.inbox}
			controller, err := req.opts.Driver.Start(taskCtx, info, emit)
			if err != nil {
				state.completedAt = time.Now()
				state.errText = err.Error()
				state.status = StatusFailed
				close(state.done)
				req.resp <- startResp{snap: m.snapshotOf(state, true), err: nil}
				close(req.resp)
				continue
			}
			state.controller = controller
			req.resp <- startResp{snap: m.snapshotOf(state, true)}
			close(req.resp)
		case emitReq:
			task := tasks[req.taskID]
			if task == nil || task.status != StatusRunning {
				continue
			}
			appendTaskOutput(task, req.data)
		case completeReq:
			task := tasks[req.taskID]
			if task == nil || task.status != StatusRunning {
				continue
			}
			task.completedAt = time.Now()
			if req.result.Output != "" {
				appendTaskOutput(task, req.result.Output)
			}
			if req.result.Error != "" {
				task.errText = req.result.Error
			}
			switch {
			case task.killRequested:
				task.status = StatusKilled
			case req.result.Error != "":
				task.status = StatusFailed
			default:
				task.status = StatusExited
			}
			close(task.done)
			if shuttingDown {
				m.maybeSignalShutdownDone(tasks)
			}
		case getReq:
			task := tasks[req.taskID]
			if task == nil {
				req.resp <- getResp{err: fmt.Errorf("task %q not found", req.taskID)}
				close(req.resp)
				continue
			}
			req.resp <- getResp{
				snap: m.snapshotOf(task, req.includeOutput),
				done: doneRef{done: task.done, ok: true},
			}
			close(req.resp)
		case listReq:
			out := make([]Snapshot, 0, len(order))
			for _, id := range order {
				if task := tasks[id]; task != nil {
					out = append(out, m.snapshotOf(task, false))
				}
			}
			req.resp <- out
			close(req.resp)
		case writeInputReq:
			task := tasks[req.taskID]
			if task == nil {
				req.resp <- fmt.Errorf("task %q not found", req.taskID)
				close(req.resp)
				continue
			}
			if task.status != StatusRunning {
				req.resp <- fmt.Errorf("task %q is not running", req.taskID)
				close(req.resp)
				continue
			}
			if task.controller == nil {
				req.resp <- ErrInputUnsupported
				close(req.resp)
				continue
			}
			err := task.controller.WriteInput(req.input)
			if err == nil {
				appendTaskOutput(task, req.input)
			}
			req.resp <- err
			close(req.resp)
		case killReq:
			task := tasks[req.taskID]
			if task == nil {
				req.resp <- terminateResp{err: fmt.Errorf("task %q not found", req.taskID)}
				close(req.resp)
				continue
			}
			if task.controller == nil {
				req.resp <- terminateResp{snap: m.snapshotOf(task, true)}
				close(req.resp)
				continue
			}
			err := task.controller.Kill()
			req.resp <- terminateResp{snap: m.snapshotOf(task, true), err: err}
			close(req.resp)
		case removeReq:
			task := tasks[req.taskID]
			if task == nil {
				req.resp <- fmt.Errorf("task %q not found", req.taskID)
				close(req.resp)
				continue
			}
			removeTask(tasks, &order, task.info.TaskID)
			req.resp <- nil
			close(req.resp)
		case shutdownReq:
			shuttingDown = true
			for _, id := range order {
				task := tasks[id]
				if task == nil || task.status != StatusRunning {
					continue
				}
				task.killRequested = true
				task.cancel()
				if task.controller != nil {
					_ = task.controller.Kill()
				}
			}
			m.maybeSignalShutdownDone(tasks)
			req.resp <- struct{}{}
			close(req.resp)
		case stopReq:
			req.resp <- struct{}{}
			close(req.resp)
			return
		}
	}
}

func (m *Manager) maybeSignalShutdownDone(tasks map[string]*taskState) {
	for _, task := range tasks {
		if task.status == StatusRunning {
			return
		}
	}
	select {
	case <-m.shutdownDone:
	default:
		close(m.shutdownDone)
	}
}

func (m *Manager) snapshotOf(task *taskState, includeOutput bool) Snapshot {
	var elapsed float64
	if task.completedAt.IsZero() {
		elapsed = time.Since(task.startedAt).Seconds()
	} else {
		elapsed = task.completedAt.Sub(task.startedAt).Seconds()
	}
	snap := Snapshot{
		TaskID:         task.info.TaskID,
		Description:    task.info.Description,
		Status:         task.status,
		Error:          task.errText,
		ElapsedSeconds: &elapsed,
	}
	if includeOutput {
		snap.Output = m.rt.TruncateTail(m.rootCtx, task.output, m.maxOutputChars)
	}
	return snap
}

func appendTaskOutput(task *taskState, text string) {
	task.output += text
}

func removeTask(tasks map[string]*taskState, order *[]string, taskID string) {
	task := tasks[taskID]
	if task == nil {
		return
	}
	if task.stopRootLink != nil {
		task.stopRootLink()
	}
	task.cancel()
	if task.status == StatusRunning && task.controller != nil {
		task.killRequested = true
		_ = task.controller.Kill()
	}
	delete(tasks, taskID)
	next := (*order)[:0]
	for _, id := range *order {
		if id != taskID {
			next = append(next, id)
		}
	}
	*order = next
}

func (m *Manager) Start(ctx context.Context, opts StartOptions) (Snapshot, error) {
	resp := make(chan startResp, 1)
	select {
	case m.inbox <- startReq{opts: opts, resp: resp}:
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
	select {
	case started := <-resp:
		return started.snap, started.err
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
}

func (m *Manager) Get(ctx context.Context, taskID string, includeOutput bool) (Snapshot, doneRef, error) {
	resp := make(chan getResp, 1)
	select {
	case m.inbox <- getReq{taskID: taskID, includeOutput: includeOutput, resp: resp}:
	case <-ctx.Done():
		return Snapshot{}, doneRef{}, ctx.Err()
	}
	got := <-resp
	return got.snap, got.done, got.err
}

func (m *Manager) Await(ctx context.Context, taskID string, timeout time.Duration) (Snapshot, error) {
	_, done, err := m.Get(ctx, taskID, false)
	if err != nil {
		return Snapshot{}, err
	}
	if done.ok {
		if timeout > 0 {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case <-done.done:
			case <-timer.C:
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			}
		} else {
			select {
			case <-done.done:
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			}
		}
	}
	snap, _, err := m.Get(ctx, taskID, true)
	return snap, err
}

func (m *Manager) List(ctx context.Context) ([]Snapshot, error) {
	resp := make(chan []Snapshot, 1)
	select {
	case m.inbox <- listReq{resp: resp}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return <-resp, nil
}

func (m *Manager) WriteInput(ctx context.Context, taskID, input string) error {
	resp := make(chan error, 1)
	select {
	case m.inbox <- writeInputReq{taskID: taskID, input: input, resp: resp}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return <-resp
}

func (m *Manager) Kill(ctx context.Context, taskID string) (Snapshot, error) {
	resp := make(chan terminateResp, 1)
	select {
	case m.inbox <- killReq{taskID: taskID, resp: resp}:
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
	got := <-resp
	return got.snap, got.err
}

func (m *Manager) Remove(ctx context.Context, taskID string) error {
	resp := make(chan error, 1)
	select {
	case m.inbox <- removeReq{taskID: taskID, resp: resp}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return <-resp
}

func (m *Manager) Shutdown(ctx context.Context) error {
	resp := make(chan struct{}, 1)
	select {
	case m.inbox <- shutdownReq{resp: resp}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-resp:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-m.shutdownDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.rootCancel()
	stopResp := make(chan struct{}, 1)
	select {
	case m.inbox <- stopReq{resp: stopResp}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-stopResp:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
