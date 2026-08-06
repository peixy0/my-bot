package events

import (
	"context"
	"time"
)

type ResponseMetadata struct {
	Model            string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	ContextWindow    int64
	GenerationTime   time.Duration
}

type Outbound interface {
	Send(ctx context.Context, text string)
	SendBegin(ctx context.Context)
	SendDelta(ctx context.Context, text string)
	SendFinal(ctx context.Context, metadata *ResponseMetadata)
	SendFull(ctx context.Context, text string, metadata *ResponseMetadata)
	StartThinking(ctx context.Context)
	EndThinking(ctx context.Context)
}

type AgentEvent interface{ agentEvent() }
type MessageEvent interface {
	messageEvent()
}
type WorkerEvent interface {
	workerEvent()
}

type TextInputEvent struct {
	ChatID    string
	MessageID string
	Message   string
	Sender    Outbound
}

type ImageData struct {
	Data     []byte
	MIMEType string
}

type ImageInputEvent struct {
	ChatID    string
	MessageID string
	ImageData []ImageData
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
	JobName  string
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
	Result chan<- error
}

type ResumeCommand struct {
	ID     string
	Result chan<- error
}

type CompressCommand struct {
	Sender Outbound
}

func (TextInputEvent) agentEvent()   {}
func (ImageInputEvent) agentEvent()  {}
func (DropSessionEvent) agentEvent() {}

func (TextInputEvent) messageEvent()   {}
func (ImageInputEvent) messageEvent()  {}
func (HeartbeatEvent) workerEvent()    {}
func (CronEvent) workerEvent()         {}
func (NewSessionEvent) workerEvent()   {}
func (ConfigQueryEvent) workerEvent()  {}
func (ConfigChangeEvent) workerEvent() {}
func (DumpCommand) workerEvent()       {}
func (ResumeCommand) workerEvent()     {}
func (CompressCommand) workerEvent()   {}

// QueuedInputEvent is used by the /queue command to wrap user input
// into a worker event that will be processed when the current work completes.
type QueuedInputEvent struct {
	ChatID    string
	MessageID string
	Message   string
	Sender    Outbound
}

func (QueuedInputEvent) workerEvent() {}
