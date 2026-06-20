package events

import "context"

type Outbound interface {
	Send(ctx context.Context, text string)
	SendDelta(ctx context.Context, text string)
	SendFinal(ctx context.Context)
	StartThinking(ctx context.Context)
	EndThinking(ctx context.Context)
}

type EventWithSender interface {
	GetSender() Outbound
}
type AgentEvent interface{ agentEvent() }
type MessageEvent interface {
	EventWithSender
	messageEvent()
}
type WorkerEvent interface {
	EventWithSender
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

const (
	ConfigKeyModel         = "model"
	ConfigKeyVision        = "vision"
	ConfigKeyTemperature   = "temperature"
	ConfigKeyTopP          = "top_p"
	ConfigKeyTopK          = "top_k"
	ConfigKeyMaxTokens     = "max_tokens"
	ConfigKeyContextWindow = "context_window"
)

type ConfigQueryEvent struct {
	ChatID string
	Key    string
	Sender Outbound
}

type ConfigChangeEvent struct {
	ChatID string
	Key    string
	Value  string
	Sender Outbound
}

type DropSessionEvent struct {
	ChatID string
}

type DumpCommand struct {
	ID     string
	Sender Outbound
}

type ResumeCommand struct {
	ID     string
	Sender Outbound
}

func (TextInputEvent) agentEvent()   {}
func (ImageInputEvent) agentEvent()  {}
func (DropSessionEvent) agentEvent() {}

func (TextInputEvent) messageEvent()   {}
func (ImageInputEvent) messageEvent()  {}
func (TextInputEvent) workerEvent()    {}
func (HeartbeatEvent) workerEvent()    {}
func (CronEvent) workerEvent()         {}
func (NewSessionEvent) workerEvent()   {}
func (ConfigQueryEvent) workerEvent()  {}
func (ConfigChangeEvent) workerEvent() {}
func (DumpCommand) workerEvent()       {}
func (ResumeCommand) workerEvent()     {}

func (e TextInputEvent) GetSender() Outbound    { return e.Sender }
func (e ImageInputEvent) GetSender() Outbound   { return e.Sender }
func (e HeartbeatEvent) GetSender() Outbound    { return e.Sender }
func (e CronEvent) GetSender() Outbound         { return e.Sender }
func (e NewSessionEvent) GetSender() Outbound   { return e.Sender }
func (e ConfigQueryEvent) GetSender() Outbound  { return e.Sender }
func (e ConfigChangeEvent) GetSender() Outbound { return e.Sender }
func (e DumpCommand) GetSender() Outbound       { return e.Sender }
func (e ResumeCommand) GetSender() Outbound     { return e.Sender }
