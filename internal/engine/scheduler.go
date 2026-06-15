package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
	"my-bot/internal/runtime"
	"my-bot/internal/tools"
)

type Scheduler struct {
	cfg        *config.Config
	agent      *llm.Agent
	rt         runtime.Runtime
	skills     *tools.SkillLoader
	inbox      inbox.Inbox[events.AgentEvent]
	cronLoader *CronLoader

	sessions map[string]*chatSession
}

func NewScheduler(
	cfg *config.Config,
	agent *llm.Agent,
	rt runtime.Runtime,
	skills *tools.SkillLoader,
	agentInbox inbox.Inbox[events.AgentEvent],
	cronLoader *CronLoader,
) *Scheduler {
	return &Scheduler{
		cfg:        cfg,
		agent:      agent,
		rt:         rt,
		skills:     skills,
		inbox:      agentInbox,
		cronLoader: cronLoader,
		sessions:   make(map[string]*chatSession),
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	defer s.closeAllSessions()
	for {
		msg, err := s.inbox.Receive(ctx)
		if err != nil {
			return err
		}
		s.dispatch(ctx, msg.Payload)
	}
}

func (s *Scheduler) dispatch(ctx context.Context, ev events.AgentEvent) {
	switch e := ev.(type) {
	case events.DropSessionEvent:
		s.closeSession(e.ChatID)
	case events.TextInputEvent:
		if cmd, ok := isSlashCommand(e.Message); ok {
			s.handleSlashCommand(ctx, cmd, e)
			return
		}
		s.dispatchUserInput(ctx, e.ChatID, e)
	case events.ImageInputEvent:
		s.dispatchUserInput(ctx, e.ChatID, e)
	default:
		if we, ok := ev.(events.WorkerEvent); ok {
			s.dispatchToSession(ctx, chatIDOf(we), we)
		}
	}
}

func (s *Scheduler) handleSlashCommand(ctx context.Context, cmd string, e events.TextInputEvent) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "heartbeat":
		interval, ok := s.heartbeatInterval(ctx, parts[1:], e.Sender)
		if !ok {
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.HeartbeatEvent{
			ChatID:          e.ChatID,
			IntervalSeconds: interval,
			Sender:          e.Sender,
		})
	case "new":
		if s.dispatchToSession(ctx, e.ChatID, events.NewSessionEvent{ChatID: e.ChatID, Sender: e.Sender}) {
			e.Sender.Send(ctx, "new session created")
		}
	case "drop":
		s.closeSession(e.ChatID)
		e.Sender.Send(ctx, fmt.Sprintf("dropped session: %s", e.ChatID))
	case "model":
		if len(parts) < 2 {
			s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{
				ChatID: e.ChatID,
				Key:    events.ConfigKeyModel,
				Sender: e.Sender,
			})
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{
			ChatID: e.ChatID,
			Key:    events.ConfigKeyModel,
			Value:  parts[1],
			Sender: e.Sender,
		})
	case "vision":
		if len(parts) < 2 {
			s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{
				ChatID: e.ChatID,
				Key:    events.ConfigKeyVision,
				Sender: e.Sender,
			})
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{
			ChatID: e.ChatID,
			Key:    events.ConfigKeyVision,
			Value:  parts[1],
			Sender: e.Sender,
		})
	case "temperature":
		if len(parts) < 2 {
			s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{
				ChatID: e.ChatID,
				Key:    events.ConfigKeyTemperature,
				Sender: e.Sender,
			})
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{
			ChatID: e.ChatID,
			Key:    events.ConfigKeyTemperature,
			Value:  parts[1],
			Sender: e.Sender,
		})
	case "top_p":
		if len(parts) < 2 {
			s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{
				ChatID: e.ChatID,
				Key:    events.ConfigKeyTopP,
				Sender: e.Sender,
			})
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{
			ChatID: e.ChatID,
			Key:    events.ConfigKeyTopP,
			Value:  parts[1],
			Sender: e.Sender,
		})
	case "top_k":
		if len(parts) < 2 {
			s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{
				ChatID: e.ChatID,
				Key:    events.ConfigKeyTopK,
				Sender: e.Sender,
			})
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{
			ChatID: e.ChatID,
			Key:    events.ConfigKeyTopK,
			Value:  parts[1],
			Sender: e.Sender,
		})
	case "max_tokens":
		if len(parts) < 2 {
			s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{
				ChatID: e.ChatID,
				Key:    events.ConfigKeyMaxTokens,
				Sender: e.Sender,
			})
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{
			ChatID: e.ChatID,
			Key:    events.ConfigKeyMaxTokens,
			Value:  parts[1],
			Sender: e.Sender,
		})
	case "context_window":
		if len(parts) < 2 {
			s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{
				ChatID: e.ChatID,
				Key:    events.ConfigKeyContextWindow,
				Sender: e.Sender,
			})
			return
		}
		s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{
			ChatID: e.ChatID,
			Key:    events.ConfigKeyContextWindow,
			Value:  parts[1],
			Sender: e.Sender,
		})
	case "queue":
		text := strings.TrimSpace(strings.TrimPrefix(cmd, "queue"))
		if text == "" {
			e.Sender.Send(ctx, "usage: /queue <message>")
			return
		}
		e.Message = text
		s.dispatchToSession(ctx, e.ChatID, e)
	case "dump":
		session, ok := s.sessions[e.ChatID]
		if !ok {
			e.Sender.Send(ctx, "no active session")
			return
		}
		if session.dump() {
			e.Sender.Send(ctx, fmt.Sprintf("session dumped to: session-%s.jsonl", e.ChatID))
		}
	case "cron":
		s.handleCronCommand(ctx, parts[1:], e)
	default:
		s.dispatchToSession(ctx, e.ChatID, e)
	}
}

func (s *Scheduler) dispatchUserInput(ctx context.Context, chatID string, e events.WorkerEvent) {
	if session, ok := s.sessions[chatID]; ok {
		if session.publishInLoop(e) {
			return
		}
	}
	s.dispatchToSession(ctx, chatID, e)
}

func (s *Scheduler) heartbeatInterval(ctx context.Context, args []string, sender events.Outbound) (int, bool) {
	if len(args) == 0 {
		return s.cfg.WakeIntervalSeconds, true
	}
	if len(args) > 1 {
		sender.Send(ctx, "usage: /heartbeat [interval-seconds]")
		return 0, false
	}
	interval, err := strconv.Atoi(args[0])
	if err != nil || interval <= 0 {
		sender.Send(ctx, "heartbeat interval must be a positive number of seconds")
		return 0, false
	}
	return interval, true
}

func (s *Scheduler) handleCronCommand(ctx context.Context, args []string, e events.TextInputEvent) {
	if len(args) == 0 {
		e.Sender.Send(ctx, "usage: /cron load|unload|ls|trigger [job-name]")
		return
	}
	sub := args[0]
	jobName := ""
	if len(args) > 1 {
		jobName = args[1]
	}
	cw := s.sessionCronWorker(ctx, e.ChatID)
	if cw == nil {
		e.Sender.Send(ctx, "cron is not configured for this session")
		return
	}

	switch sub {
	case "load":
		if jobName == "" {
			e.Sender.Send(ctx, "usage: /cron load <job-name>")
			return
		}
		defs, err := cw.Load(jobName, e.Sender)
		if err != nil {
			e.Sender.Send(ctx, fmt.Sprintf("error: %v", err))
			return
		}
		e.Sender.Send(ctx, fmt.Sprintf("loaded %d tasks for job %q", len(defs), jobName))
	case "unload":
		if jobName == "" {
			e.Sender.Send(ctx, "usage: /cron unload <job-name>")
			return
		}
		if cw.Unload(jobName) {
			e.Sender.Send(ctx, fmt.Sprintf("unloaded %q", jobName))
		} else {
			e.Sender.Send(ctx, fmt.Sprintf("job %q not loaded", jobName))
		}
	case "trigger":
		if jobName == "" {
			e.Sender.Send(ctx, "usage: /cron trigger <job-name>")
			return
		}
		err := cw.Trigger(jobName, e.Sender)
		if err == nil {
			e.Sender.Send(ctx, fmt.Sprintf("job %q triggered", jobName))
		} else {
			e.Sender.Send(ctx, fmt.Sprintf("failed to trigger job %q: %v", jobName, err))
		}
	case "ls":
		available := s.cronLoader.ListJobs()
		if len(available) == 0 {
			e.Sender.Send(ctx, "no cron jobs found in .cron/")
			return
		}
		loaded := make(map[string]struct{})
		for _, name := range cw.LoadedJobs() {
			loaded[name] = struct{}{}
		}
		var lines []string
		for _, job := range available {
			defs, _ := s.cronLoader.LoadJob(job)
			status := ""
			if _, ok := loaded[job]; ok {
				status = " [loaded]"
			}
			var taskLines []string
			for _, d := range defs {
				taskLines = append(taskLines, fmt.Sprintf("  - %s (%s)", d.TaskName, d.CronExpr))
			}
			lines = append(lines, job+status+"\n"+strings.Join(taskLines, "\n"))
		}
		e.Sender.Send(ctx, "available cron jobs:\n\n"+strings.Join(lines, "\n\n"))
	default:
		e.Sender.Send(ctx, "usage: /cron load|unload|ls [job-name]")
	}
}

func (s *Scheduler) getOrCreateSession(ctx context.Context, chatID string) *chatSession {
	if session, ok := s.sessions[chatID]; ok {
		return session
	}
	session := newChatSession(ctx, chatID, s.cfg, s.agent, s.rt, s.skills, s.cronLoader)
	s.sessions[chatID] = session
	return session
}

func (s *Scheduler) dispatchToSession(ctx context.Context, chatID string, ev events.WorkerEvent) bool {
	if chatID == "" {
		slog.Error("worker event dropped: missing chat id", "event", fmt.Sprintf("%T", ev))
		return false
	}
	return s.getOrCreateSession(ctx, chatID).publish(ev)
}

func sendWorkerEvent(chatID string, workerInbox inbox.Inbox[events.WorkerEvent], ev events.WorkerEvent) bool {
	if workerInbox.TryPublish(workerEnvelope(chatID, ev)) {
		return true
	}
	slog.Error("worker event dropped: events channel full", "chat", chatID, "event", fmt.Sprintf("%T", ev))
	return false
}

func (s *Scheduler) closeSession(chatID string) {
	if session, ok := s.sessions[chatID]; ok {
		delete(s.sessions, chatID)
		session.close()
	}
}

func (s *Scheduler) sessionCronWorker(ctx context.Context, chatID string) *CronWorker {
	return s.getOrCreateSession(ctx, chatID).cronWorker()
}

func (s *Scheduler) closeAllSessions() {
	for chatID := range s.sessions {
		s.closeSession(chatID)
	}
}

func isSlashCommand(msg string) (string, bool) {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return "", false
	}
	return strings.TrimPrefix(msg, "/"), true
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func chatIDOf(e events.WorkerEvent) string {
	switch ev := e.(type) {
	case events.TextInputEvent:
		return ev.ChatID
	case events.ImageInputEvent:
		return ev.ChatID
	case events.HeartbeatEvent:
		return ev.ChatID
	case events.CronEvent:
		return ev.ChatID
	case events.NewSessionEvent:
		return ev.ChatID
	case events.ConfigQueryEvent:
		return ev.ChatID
	case events.ConfigChangeEvent:
		return ev.ChatID
	}
	return ""
}
