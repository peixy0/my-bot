package engine

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
	"my-bot/internal/runtime"
	"my-bot/internal/tools"
)

const (
	workerEventBuf  = 64
	controlEventBuf = 64
	messageInboxBuf = 32
)

type ConversationWorker struct {
	chatID  string
	cfg     *config.Config
	baseLLM config.LLMConfig
	agent   *Agent
	rt      runtime.Runtime
	skills  *tools.SkillLoader
	loop    *AgentLoop

	tools *sessionTools

	Events       *inbox.Memory[events.WorkerEvent]
	Control      *inbox.Memory[events.WorkerEvent]
	MessageInbox *inbox.Memory[events.MessageEvent]

	abortCh chan struct{}

	heartbeatTimer *time.Timer
	lastHeartbeat  *events.HeartbeatEvent
}

func newConversationWorker(
	chatID string,
	env SessionEnv,
	tools *sessionTools,
) *ConversationWorker {
	w := &ConversationWorker{
		chatID:       chatID,
		cfg:          env.Cfg,
		baseLLM:      env.Cfg.BaseLLM(),
		agent:        env.Agent,
		rt:           env.Rt,
		skills:       env.Skills,
		tools:        tools,
		Events:       inbox.NewMemory[events.WorkerEvent](workerEventBuf),
		Control:      inbox.NewMemory[events.WorkerEvent](controlEventBuf),
		MessageInbox: inbox.NewMemory[events.MessageEvent](messageInboxBuf),
		abortCh:      make(chan struct{}),
	}
	w.loop = NewAgentLoop(w.cfg, env.Agent)
	return w
}

func (w *ConversationWorker) Model() string {
	return w.cfg.LLM.Model
}

func (w *ConversationWorker) SetModel(model string) {
	w.cfg.LLM = w.baseLLM
	w.cfg.LLM.Model = model
	if preset := w.cfg.FindPreset(model); preset != nil {
		preset.ApplyTo(&w.cfg.LLM)
	}
}

func (w *ConversationWorker) VisionSupported() bool {
	return w.cfg.LLM.Vision
}

func (w *ConversationWorker) SetVisionSupported(enabled bool) {
	w.cfg.LLM.Vision = enabled
}

func (w *ConversationWorker) Temperature() string {
	return formatFloat(w.cfg.LLM.Temperature)
}

func (w *ConversationWorker) SetTemperature(value float64) {
	w.cfg.LLM.Temperature = value
}

func (w *ConversationWorker) MaxTokens() string {
	return strconv.FormatInt(w.cfg.Context.MaxOutputTokens, 10)
}

func (w *ConversationWorker) SetMaxTokens(value int64) {
	w.cfg.Context.MaxOutputTokens = value
}

func (w *ConversationWorker) ContextWindow() string {
	return strconv.FormatInt(w.cfg.LLM.ContextWindow, 10)
}

func (w *ConversationWorker) SetContextWindow(value int64) {
	w.cfg.LLM.ContextWindow = value
}

func (w *ConversationWorker) Run(ctx context.Context) error {
	slog.Debug("worker started", "chat_id", w.chatID)
	for {
		if event, ok := w.Control.TryReceive(); ok {
			if w.handleControlEvent(ctx, event) {
				return nil
			}
			continue
		}
		select {
		case event := <-w.Control.C():
			if w.handleControlEvent(ctx, event) {
				return nil
			}
		case e := <-w.Events.C():
			w.stopHeartbeat()
			if hb, ok := e.(events.HeartbeatEvent); ok {
				w.lastHeartbeat = &hb
			}
			if err := w.handleEvent(ctx, e); err != nil {
				slog.Error("event handler", "chat_id", w.chatID, "event", fmt.Sprintf("%T", e), "err", err)
			}
			w.scheduleHeartbeat()
		case e := <-w.MessageInbox.C():
			messages := drainMessageInbox(w.MessageInbox, e)
			slog.Debug("message batch", "chat_id", w.chatID, "count", len(messages))
			w.stopHeartbeat()
			if err := w.processMessages(ctx, messages); err != nil {
				slog.Error("message input", "chat_id", w.chatID, "err", err)
			}
			w.scheduleHeartbeat()
		case <-ctx.Done():
			slog.Debug("worker context done", "chat_id", w.chatID)
			return ctx.Err()
		}
	}
}

func (w *ConversationWorker) handleControlEvent(ctx context.Context, event events.WorkerEvent) bool {
	w.stopHeartbeat()
	exit, err := w.processControlEvent(ctx, event)
	if err != nil {
		slog.Error("control event handler", "chat_id", w.chatID, "event", fmt.Sprintf("%T", event), "err", err)
	}
	if !exit {
		w.scheduleHeartbeat()
	}
	return exit
}

func (w *ConversationWorker) processControlEvent(ctx context.Context, event events.WorkerEvent) (bool, error) {
	switch ev := event.(type) {
	case events.NewSessionEvent:
		return false, w.processNewSession(ctx, ev)
	case events.ConfigQueryEvent:
		return false, w.processConfigQuery(ctx, ev)
	case events.ConfigChangeEvent:
		return false, w.processConfigChange(ctx, ev)
	case events.DumpCommand:
		return false, w.processDump(ctx, ev)
	case events.ResumeCommand:
		return false, w.processResume(ctx, ev)
	case events.RebootCommand:
		w.processReboot(ev)
		return true, nil
	default:
		return false, fmt.Errorf("unexpected control event type %T", event)
	}
}

func (w *ConversationWorker) handleEvent(ctx context.Context, e events.WorkerEvent) error {
	switch ev := e.(type) {
	case events.HeartbeatEvent:
		return w.processHeartbeat(ctx, ev)
	case events.CronEvent:
		return w.processCron(ctx, ev)
	case events.CompressCommand:
		return w.processCompress(ctx, ev)
	case events.QueuedInputEvent:
		return w.processMessages(ctx, []events.MessageEvent{events.TextInputEvent(ev)})
	default:
		return fmt.Errorf("unexpected event type %T", e)
	}
}

func (w *ConversationWorker) processMessages(ctx context.Context, messages []events.MessageEvent) error {
	if len(messages) == 0 {
		return nil
	}
	for _, message := range messages {
		switch ev := message.(type) {
		case events.TextInputEvent:
			slog.Debug("text input", "chat_id", w.chatID, "msg_id", ev.MessageID, "len", len(ev.Message), "content", ev.Message)
		case events.ImageInputEvent:
			totalBytes := 0
			for _, image := range ev.ImageData {
				totalBytes += len(image.Data)
			}
			slog.Debug("image input", "chat_id", w.chatID, "msg_id", ev.MessageID, "count", len(ev.ImageData), "bytes", totalBytes)
		default:
			return fmt.Errorf("unexpected message type %T", message)
		}
	}

	sender := messageEventSender(messages[0])
	batch := buildMessageBatch(messages, w.VisionSupported(), messageTime())
	if batch == nil {
		sender.Send(ctx, "Message cannot be processed for the current model")
		return nil
	}
	prompt := llm.NewMainPrompt(w.skills, w.rt)
	if err := w.maybeCompress(ctx); err != nil {
		slog.Error("compress", "chat_id", w.chatID, "err", err)
	}
	reg := w.tools.BuildRegistry(sender)
	orch := NewHumanInputOrchestrator(sender, w.MessageInbox).WithVision(w.VisionSupported())
	sender.StartThinking(ctx)
	defer sender.EndThinking(ctx)
	err := w.loop.Run(ctx, w.abortCh, reg, orch, prompt, batch.Content)
	if err != nil {
		sender.Send(ctx, errorMessage(err))
	}
	return err
}

func (w *ConversationWorker) processReboot(request events.RebootCommand) {
	w.stopHeartbeat()
	err := w.loop.DumpConversation(w.conversationPath(request.ID))
	select {
	case request.Result <- err:
	default:
	}
}

func (w *ConversationWorker) processHeartbeat(ctx context.Context, ev events.HeartbeatEvent) error {
	slog.Debug("heartbeat start", "chat_id", w.chatID, "interval_s", ev.IntervalSeconds)
	err := w.runBackground(ctx, ev.Sender, llm.NewHeartbeatPrompt(w.skills, w.rt), wrapUserMessage("SYSTEM EVENT: heartbeat"))
	slog.Debug("heartbeat end", "chat_id", w.chatID, "err", err)
	return err
}

func (w *ConversationWorker) processCron(ctx context.Context, ev events.CronEvent) error {
	slog.Debug("cron start", "chat_id", w.chatID, "job", ev.JobName, "task", ev.TaskName)
	prompt := fmt.Sprintf("SYSTEM EVENT: scheduled task '%s'\n\n%s", ev.TaskName, ev.Prompt)
	err := w.runBackground(ctx, ev.Sender, llm.NewCronPrompt(w.skills, w.rt), wrapUserMessage(prompt))
	slog.Debug("cron end", "chat_id", w.chatID, "task", ev.TaskName, "err", err)
	return err
}

func (w *ConversationWorker) processNewSession(ctx context.Context, ev events.NewSessionEvent) error {
	slog.Debug("new session", "chat_id", w.chatID)
	w.loop.ResetConv()
	return nil
}

func (w *ConversationWorker) processDump(_ context.Context, ev events.DumpCommand) error {
	err := w.loop.DumpConversation(w.conversationPath(ev.ID))
	select {
	case ev.Result <- err:
	default:
	}
	return err
}

func (w *ConversationWorker) processResume(_ context.Context, ev events.ResumeCommand) error {
	err := w.loop.LoadConversation(w.conversationPath(ev.ID))
	select {
	case ev.Result <- err:
	default:
	}
	return err
}

func (w *ConversationWorker) processCompress(ctx context.Context, ev events.CompressCommand) error {
	slog.Debug("context compression start", "chat_id", w.chatID)
	ev.Sender.StartThinking(ctx)
	ev.Sender.Send(ctx, "compressing context")
	defer ev.Sender.EndThinking(ctx)
	if err := w.loop.Compress(ctx); err != nil {
		ev.Sender.Send(ctx, fmt.Sprintf("error: %v", err))
		return err
	}
	ev.Sender.Send(ctx, "context compressed")
	slog.Debug("context compression done", "chat_id", w.chatID)
	return nil
}

func (w *ConversationWorker) processConfigQuery(ctx context.Context, ev events.ConfigQueryEvent) error {
	switch ev.Key {
	case events.ConfigKeyModel:
		ev.Sender.Send(ctx, fmt.Sprintf("current model: %s", w.Model()))
	case events.ConfigKeyVision:
		ev.Sender.Send(ctx, fmt.Sprintf("current vision: %s", onOff(w.VisionSupported())))
	case events.ConfigKeyTemperature:
		ev.Sender.Send(ctx, fmt.Sprintf("current temperature: %s", w.Temperature()))
	case events.ConfigKeyMaxTokens:
		ev.Sender.Send(ctx, fmt.Sprintf("current max_tokens: %s", w.MaxTokens()))
	case events.ConfigKeyContextWindow:
		ev.Sender.Send(ctx, fmt.Sprintf("current context_window: %s", w.ContextWindow()))
	}
	return nil
}

func (w *ConversationWorker) processConfigChange(ctx context.Context, ev events.ConfigChangeEvent) error {
	switch ev.Key {
	case events.ConfigKeyModel:
		w.SetModel(ev.Value)
		msg := fmt.Sprintf("model set to: %s", ev.Value)
		if w.cfg.FindPreset(ev.Value) != nil {
			msg += " (preset applied)"
		}
		ev.Sender.Send(ctx, msg)
	case events.ConfigKeyVision:
		switch ev.Value {
		case "on":
			w.SetVisionSupported(true)
			ev.Sender.Send(ctx, "vision set to: on")
		case "off":
			w.SetVisionSupported(false)
			ev.Sender.Send(ctx, "vision set to: off")
		default:
			ev.Sender.Send(ctx, "usage: /vision on|off")
		}
	case events.ConfigKeyTemperature:
		value, err := strconv.ParseFloat(ev.Value, 64)
		if err != nil || value < 0 || value > 2 {
			ev.Sender.Send(ctx, "usage: /temperature <0..2>")
			return nil
		}
		w.SetTemperature(value)
		ev.Sender.Send(ctx, fmt.Sprintf("temperature set to: %s", formatFloat(value)))
	case events.ConfigKeyMaxTokens:
		value, err := strconv.ParseInt(ev.Value, 10, 64)
		if err != nil || value <= 0 {
			ev.Sender.Send(ctx, "usage: /max_tokens <positive integer>")
			return nil
		}
		w.SetMaxTokens(value)
		ev.Sender.Send(ctx, fmt.Sprintf("max_tokens set to: %d", value))
	case events.ConfigKeyContextWindow:
		value, err := strconv.ParseInt(ev.Value, 10, 64)
		if err != nil || value <= 0 {
			ev.Sender.Send(ctx, "usage: /context_window <positive integer>")
			return nil
		}
		w.SetContextWindow(value)
		ev.Sender.Send(ctx, fmt.Sprintf("context_window set to: %d", value))
	}
	return nil
}

func (w *ConversationWorker) runBackground(ctx context.Context, sender events.Outbound, prompt llm.SystemPrompt, content any) error {
	reg := w.tools.BuildRegistry(sender)
	orch := NewBackgroundOrchestrator(sender)
	err := w.loop.Run(ctx, w.abortCh, reg, orch, prompt, content)
	if err != nil {
		sender.Send(ctx, errorMessage(err))
	}
	return err
}

func (w *ConversationWorker) maybeCompress(ctx context.Context) error {
	threshold := int(float64(w.cfg.LLM.ContextWindow) * w.cfg.Context.CompressionThreshold)
	if threshold <= 0 || int(w.loop.TotalTokens()) < threshold {
		return nil
	}
	slog.Debug("compressing context", "chat_id", w.chatID, "tokens", w.loop.TotalTokens(), "threshold", threshold)
	return w.loop.Compress(ctx)
}

func (w *ConversationWorker) scheduleHeartbeat() {
	if w.lastHeartbeat == nil {
		return
	}
	hb := *w.lastHeartbeat
	interval := time.Duration(hb.IntervalSeconds) * time.Second
	w.heartbeatTimer = time.AfterFunc(interval, func() {
		if !w.Events.TryPublish(hb) {
			slog.Warn("heartbeat dropped: events channel full", "chat_id", w.chatID)
		}
	})
}

func (w *ConversationWorker) stopHeartbeat() {
	if w.heartbeatTimer != nil {
		w.heartbeatTimer.Stop()
		w.heartbeatTimer = nil
	}
}

func errorMessage(err error) string {
	if err == ErrAborted {
		return "session aborted"
	}
	return fmt.Sprintf("error: %v", err)
}

func (w *ConversationWorker) conversationPath(id string) string {
	return filepath.Join(w.cfg.Workspace.SessionDir, id+".json")
}

func wrapUserMessage(msg string) string {
	return messageTime() + "\n\n" + msg
}

func messageTime() string {
	return fmt.Sprintf("MESSAGE TIME: %s", time.Now().Format("Monday, 02 Jan 2006 15:04:05 -0700"))
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
