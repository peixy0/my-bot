package events

import "context"

type Outbound interface {
	Send(ctx context.Context, text string)
	SendDelta(ctx context.Context, text string)
	SendFinal(ctx context.Context)
	StartThinking(ctx context.Context)
	EndThinking(ctx context.Context)
}

type AgentEvent interface{ agentEvent() }
type WorkerEvent interface {
	workerEvent()
}

type TextInputEvent struct {
	ChatID    string
	MessageID string
	Message   string
	Sender    Outbound
}

type ImageInputEvent struct {
	ChatID    string
	MessageID string
	ImageData []byte
	MIMEType  string
	Message   string
	Sender    Outbound
}

type HeartbeatEvent struct {
	ChatID          string
	IntervalSeconds int
	Sender          Outbound
}

type CronEvent struct {
	ChatID   string
	TaskName string
	Prompt   string
	Sender   Outbound
}

type NewSessionEvent struct {
	ChatID string
	Sender Outbound
}

type DropSessionEvent struct {
	ChatID string
}

type SteerMessage struct {
	MessageID string
	Text      string
	Sender    Outbound
}

type DumpCommand struct {
}

func (TextInputEvent) agentEvent()   {}
func (ImageInputEvent) agentEvent()  {}
func (CronEvent) agentEvent()        {}
func (DumpCommand) agentEvent()      {}
func (DropSessionEvent) agentEvent() {}

func (TextInputEvent) workerEvent()  {}
func (ImageInputEvent) workerEvent() {}
func (HeartbeatEvent) workerEvent()  {}
func (CronEvent) workerEvent()       {}
func (NewSessionEvent) workerEvent() {}
func (DumpCommand) workerEvent()     {}
