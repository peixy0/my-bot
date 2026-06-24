package engine

import (
	"context"
	"encoding/base64"
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
	messageInboxBuf = 32
)

type ConversationWorker struct {
	chatID string
	cfg    *config.Config
	agent  *Agent
	rt     runtime.Runtime
	skills *tools.SkillLoader
	loop   *AgentLoop

	tools *sessionTools

	Events       *inbox.Memory[events.WorkerEvent]
	MessageInbox *inbox.Memory[events.MessageEvent]

	abortCh chan struct{}

	heartbeatTimer *time.Timer
	lastHeartbeat  *events.HeartbeatEvent
}

func newConversationWorker(
	chatID string,
	cfg *config.Config,
	agent *Agent,
	rt runtime.Runtime,
	skills *tools.SkillLoader,
	tools *sessionTools,
) *ConversationWorker {
	w := &ConversationWorker{
		chatID:       chatID,
		cfg:          cfg,
		agent:        agent,
		rt:           rt,
		skills:       skills,
		tools:        tools,
		Events:       inbox.NewMemory[events.WorkerEvent](workerEventBuf),
		MessageInbox: inbox.NewMemory[events.MessageEvent](messageInboxBuf),
		abortCh:      make(chan struct{}),
	}
	w.loop = NewAgentLoop(w.cfg, agent)
	return w
}

func (w *ConversationWorker) Model() string {
	return w.cfg.LLM.Model
}

func (w *ConversationWorker) SetModel(model string) {
	w.cfg.LLM.Model = model
	if preset, ok := w.cfg.Models[model]; ok {
		preset.ApplyTo(&w.cfg.LLM)
	}
}

func (w *ConversationWorker) VisionSupported() bool {
	return w.cfg.Vision.Enabled
}

func (w *ConversationWorker) SetVisionSupported(enabled bool) {
	w.cfg.Vision.Enabled = enabled
}

func (w *ConversationWorker) Temperature() string {
	return formatFloat(w.cfg.LLM.Temperature)
}

func (w *ConversationWorker) SetTemperature(value float64) {
	w.cfg.LLM.Temperature = value
}

func (w *ConversationWorker) TopP() string {
	return formatFloat(w.cfg.LLM.TopP)
}

func (w *ConversationWorker) SetTopP(value float64) {
	w.cfg.LLM.TopP = value
}

func (w *ConversationWorker) TopK() string {
	if w.cfg.LLM.TopK <= 0 {
		return "unset"
	}
	return strconv.Itoa(w.cfg.LLM.TopK)
}

func (w *ConversationWorker) SetTopK(value int) {
	w.cfg.LLM.TopK = value
}

func (w *ConversationWorker) MaxTokens() string {
	return strconv.FormatInt(w.cfg.Context.MaxOutputTokens, 10)
}

func (w *ConversationWorker) SetMaxTokens(value int64) {
	w.cfg.Context.MaxOutputTokens = value
}

func (w *ConversationWorker) ContextWindow() string {
	return strconv.FormatInt(w.cfg.Context.WindowTokens, 10)
}

func (w *ConversationWorker) SetContextWindow(value int64) {
	w.cfg.Context.WindowTokens = value
}

func (w *ConversationWorker) Run(ctx context.Context) error {
	slog.Debug("worker started", "chat_id", w.chatID)
	for {
		select {
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
			slog.Debug("message input", "chat_id", w.chatID, "event", fmt.Sprintf("%T", e))
			w.stopHeartbeat()
			if err := w.handleMessage(ctx, e); err != nil {
				slog.Error("message input", "chat_id", w.chatID, "err", err)
			}
			w.scheduleHeartbeat()
		case <-ctx.Done():
			slog.Debug("worker context done", "chat_id", w.chatID)
			return ctx.Err()
		}
	}
}

func (w *ConversationWorker) handleEvent(ctx context.Context, e events.WorkerEvent) error {
	switch ev := e.(type) {
	case events.HeartbeatEvent:
		return w.processHeartbeat(ctx, ev)
	case events.CronEvent:
		return w.processCron(ctx, ev)
	case events.NewSessionEvent:
		return w.processNewSession(ctx, ev)
	case events.ConfigQueryEvent:
		return w.processConfigQuery(ctx, ev)
	case events.ConfigChangeEvent:
		return w.processConfigChange(ctx, ev)
	case events.DumpCommand:
		return w.processDump(ctx, ev)
	case events.ResumeCommand:
		return w.processResume(ctx, ev)
	case events.QueuedInputEvent:
		return w.processText(ctx, events.TextInputEvent(ev))
	default:
		return fmt.Errorf("unexpected event type %T", e)
	}
}

func (w *ConversationWorker) handleMessage(ctx context.Context, e events.MessageEvent) error {
	switch ev := e.(type) {
	case events.TextInputEvent:
		return w.processText(ctx, ev)
	case events.ImageInputEvent:
		return w.processImage(ctx, ev)
	default:
		return fmt.Errorf("unexpected event type %T", e)
	}
}

func (w *ConversationWorker) processText(ctx context.Context, ev events.TextInputEvent) error {
	slog.Debug("text input", "chat_id", w.chatID, "msg_id", ev.MessageID, "len", len(ev.Message), "content", ev.Message)
	prompt := llm.NewMainPrompt(w.skills, w.rt)
	if err := w.maybeCompress(ctx, prompt); err != nil {
		slog.Error("compress", "chat_id", w.chatID, "err", err)
	}
	reg := w.tools.BuildRegistry(ev.Sender)
	orch := NewHumanInputOrchestrator(reg, ev.Sender, w.MessageInbox).WithVision(w.VisionSupported())
	ev.Sender.StartThinking(ctx)
	defer ev.Sender.EndThinking(ctx)
	err := w.loop.Run(ctx, w.abortCh, reg, orch, prompt, wrapUserMessage(ev.Message))
	if err != nil {
		ev.Sender.Send(ctx, errorMessage(err))
	}
	return err
}

func (w *ConversationWorker) processImage(ctx context.Context, ev events.ImageInputEvent) error {
	if !w.VisionSupported() {
		ev.Sender.Send(ctx, "Image processing is disabled for the current model")
		return nil
	}
	slog.Debug("image input", "chat_id", w.chatID, "msg_id", ev.MessageID, "mime", ev.MIMEType, "bytes", len(ev.ImageData))
	prompt := llm.NewMainPrompt(w.skills, w.rt)
	if err := w.maybeCompress(ctx, prompt); err != nil {
		slog.Error("compress", "chat_id", w.chatID, "err", err)
	}
	content := []map[string]any{
		{"type": "text", "text": wrapUserMessage(ev.Message)},
		{
			"type": "image_url",
			"image_url": map[string]any{
				"url":    fmt.Sprintf("data:%s;base64,%s", ev.MIMEType, base64.StdEncoding.EncodeToString(ev.ImageData)),
				"detail": "auto",
			},
		},
	}
	reg := w.tools.BuildRegistry(ev.Sender)
	orch := NewHumanInputOrchestrator(reg, ev.Sender, w.MessageInbox).WithVision(true)
	ev.Sender.StartThinking(ctx)
	defer ev.Sender.EndThinking(ctx)
	err := w.loop.Run(ctx, w.abortCh, reg, orch, prompt, content)
	if err != nil {
		ev.Sender.Send(ctx, errorMessage(err))
	}
	return err
}

func (w *ConversationWorker) processHeartbeat(ctx context.Context, ev events.HeartbeatEvent) error {
	slog.Debug("heartbeat start", "chat_id", w.chatID, "interval_s", ev.IntervalSeconds)
	err := w.runBackground(ctx, ev.Sender, llm.NewHeartbeatPrompt(w.skills, w.rt), wrapUserMessage("SYSTEM EVENT: heartbeat"))
	slog.Debug("heartbeat end", "chat_id", w.chatID, "err", err)
	return err
}

func (w *ConversationWorker) processCron(ctx context.Context, ev events.CronEvent) error {
	slog.Debug("cron start", "chat_id", w.chatID, "task", ev.TaskName)
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

func (w *ConversationWorker) processDump(ctx context.Context, ev events.DumpCommand) error {
	path := w.conversationPath(ev.ID)
	if err := w.loop.DumpConversation(path); err != nil {
		ev.Sender.Send(ctx, fmt.Sprintf("error: %v", err))
		return err
	}
	ev.Sender.Send(ctx, fmt.Sprintf("session dumped, load with: /resume %s", ev.ID))
	return nil
}

func (w *ConversationWorker) processResume(ctx context.Context, ev events.ResumeCommand) error {
	path := w.conversationPath(ev.ID)
	if err := w.loop.LoadConversation(path); err != nil {
		ev.Sender.Send(ctx, fmt.Sprintf("error: %v", err))
		return err
	}
	ev.Sender.Send(ctx, fmt.Sprintf("session resumed: %s", ev.ID))
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
	case events.ConfigKeyTopP:
		ev.Sender.Send(ctx, fmt.Sprintf("current top_p: %s", w.TopP()))
	case events.ConfigKeyTopK:
		ev.Sender.Send(ctx, fmt.Sprintf("current top_k: %s", w.TopK()))
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
		if _, ok := w.cfg.Models[ev.Value]; ok {
			msg += " (model preset applied)"
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
	case events.ConfigKeyTopP:
		value, err := strconv.ParseFloat(ev.Value, 64)
		if err != nil || value < 0 || value > 1 {
			ev.Sender.Send(ctx, "usage: /top_p <0..1>")
			return nil
		}
		w.SetTopP(value)
		ev.Sender.Send(ctx, fmt.Sprintf("top_p set to: %s", formatFloat(value)))
	case events.ConfigKeyTopK:
		value, err := strconv.Atoi(ev.Value)
		if err != nil || value <= 0 {
			ev.Sender.Send(ctx, "usage: /top_k <positive integer>")
			return nil
		}
		w.SetTopK(value)
		ev.Sender.Send(ctx, fmt.Sprintf("top_k set to: %d", value))
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
	orch := NewBackgroundOrchestrator(reg, sender)
	return w.loop.Run(ctx, nil, reg, orch, prompt, content)
}

func (w *ConversationWorker) maybeCompress(ctx context.Context, prompt llm.SystemPrompt) error {
	if !w.cfg.Context.AutoCompression {
		return nil
	}
	threshold := int(float64(w.cfg.Context.WindowTokens) * w.cfg.Context.CompressionThreshold)
	if threshold <= 0 || int(w.loop.TotalTokens()) < threshold {
		return nil
	}
	slog.Debug("compressing context", "chat_id", w.chatID, "tokens", w.loop.TotalTokens(), "threshold", threshold)
	return w.loop.Compress(ctx, prompt)
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
	now := time.Now()
	return fmt.Sprintf("MESSAGE TIME: %s\n\n%s",
		now.Format("2006-01-02 15:04:05 MST-0700"),
		msg)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
