package tasks

import (
	"context"
	"errors"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusExited  Status = "exited"
	StatusFailed  Status = "failed"
	StatusKilled  Status = "killed"
)

type Snapshot struct {
	TaskID         string   `json:"task_id"`
	Description    string   `json:"description"`
	Status         Status   `json:"status"`
	Error          string   `json:"error,omitempty"`
	ElapsedSeconds *float64 `json:"elapsed_seconds,omitempty"`
	Output         string   `json:"output,omitempty"`
}

type TaskInfo struct {
	TaskID      string
	Description string
}

type TaskResult struct {
	Output string
	Error  string
}

type Controller interface {
	WriteInput(string) error
	Kill() error
}

type Driver interface {
	Start(context.Context, TaskInfo, *Emitter) (Controller, error)
}

type StartOptions struct {
	Description string
	Driver      Driver
}

type Emitter struct {
	taskID string
	inbox  chan<- any
}

func (e *Emitter) Output(text string) {
	if text == "" {
		return
	}
	e.inbox <- emitReq{taskID: e.taskID, data: text}
}

func (e *Emitter) Complete(result TaskResult) {
	e.inbox <- completeReq{taskID: e.taskID, result: result}
}

type doneRef struct {
	done <-chan struct{}
	ok   bool
}

var ErrInputUnsupported = errors.New("task does not accept input")
