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

type workerEntry struct {
	worker *ConversationWorker
	cancel context.CancelFunc
}

type Scheduler struct {
	cfg        *config.Config
	agent      *llm.Agent
	rt         runtime.Runtime
	skills     *tools.SkillLoader
	inbox      inbox.Inbox[events.AgentEvent]
	cronLoader *CronLoader

	workers     map[string]*workerEntry
	cronWorkers map[string]*CronWorker
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
		cfg:         cfg,
		agent:       agent,
		rt:          rt,
		skills:      skills,
		inbox:       agentInbox,
		cronLoader:  cronLoader,
		workers:     make(map[string]*workerEntry),
		cronWorkers: make(map[string]*CronWorker),
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
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
		s.dropWorker(e.ChatID)
	case events.TextInputEvent:
		if cmd, ok := isSlashCommand(e.Message); ok {
			s.handleSlashCommand(ctx, cmd, e)
			return
		}
		s.dispatchToWorker(ctx, e.ChatID, e)
	default:
		if we, ok := ev.(events.WorkerEvent); ok {
			s.dispatchToWorker(ctx, chatIDOf(we), we)
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
		s.dispatchToWorker(ctx, e.ChatID, events.HeartbeatEvent{
			ChatID:          e.ChatID,
			IntervalSeconds: interval,
			Sender:          e.Sender,
		})
	case "new":
		if s.dispatchToWorker(ctx, e.ChatID, events.NewSessionEvent{ChatID: e.ChatID, Sender: e.Sender}) {
			e.Sender.Send(ctx, "new session created")
		}
	case "drop":
		s.dropWorker(e.ChatID)
		e.Sender.Send(ctx, fmt.Sprintf("dropped session: %s", e.ChatID))
	case "model":
		worker := s.getOrCreate(ctx, e.ChatID)
		if len(parts) < 2 {
			e.Sender.Send(ctx, fmt.Sprintf("current model: %s", worker.Model()))
			return
		}
		worker.SetModel(parts[1])
		e.Sender.Send(ctx, fmt.Sprintf("model set to: %s", parts[1]))
	case "steer":
		text := strings.TrimSpace(strings.TrimPrefix(cmd, "steer"))
		entry, ok := s.workers[e.ChatID]
		if !ok {
			e.Sender.Send(ctx, "no active session")
			return
		}
		if entry.worker.InLoopInbox.TryPublish(steerEnvelope(e.ChatID, events.SteerMessage{
			MessageID: e.MessageID,
			Text:      text,
			Sender:    e.Sender,
		})) {
			return
		}
		s.dispatchToWorker(ctx, e.ChatID, e)
	case "dump":
		entry, ok := s.workers[e.ChatID]
		if !ok {
			e.Sender.Send(ctx, "no active session")
			return
		}
		if sendWorkerEvent(e.ChatID, entry.worker.Events, events.DumpCommand{}) {
			e.Sender.Send(ctx, fmt.Sprintf("session dumped to: session-%s.jsonl", e.ChatID))
		}
	case "cron":
		s.handleCronCommand(ctx, parts[1:], e)
	default:
		s.dispatchToWorker(ctx, e.ChatID, e)
	}
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
		e.Sender.Send(ctx, "usage: /cron load|unload|ls [job-name]")
		return
	}
	sub := args[0]
	jobName := ""
	if len(args) > 1 {
		jobName = args[1]
	}
	cw := s.getCronWorker(ctx, e.ChatID)

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

func (s *Scheduler) getOrCreate(ctx context.Context, chatID string) *ConversationWorker {
	if e, ok := s.workers[chatID]; ok {
		return e.worker
	}
	w := NewConversationWorker(chatID, s.cfg, s.agent, s.rt, s.skills)
	workerCtx, cancel := context.WithCancel(ctx)
	s.workers[chatID] = &workerEntry{worker: w, cancel: cancel}
	go func() {
		if err := w.Run(workerCtx); err != nil && err != context.Canceled {
			slog.Error("worker exited", "chat", chatID, "err", err)
		}
	}()
	return w
}

func (s *Scheduler) dispatchToWorker(ctx context.Context, chatID string, ev events.WorkerEvent) bool {
	if chatID == "" {
		slog.Error("worker event dropped: missing chat id", "event", fmt.Sprintf("%T", ev))
		return false
	}
	return sendWorkerEvent(chatID, s.getOrCreate(ctx, chatID).Events, ev)
}

func sendWorkerEvent(chatID string, workerInbox inbox.Inbox[events.WorkerEvent], ev events.WorkerEvent) bool {
	if workerInbox.TryPublish(workerEnvelope(chatID, ev)) {
		return true
	}
	slog.Error("worker event dropped: events channel full", "chat", chatID, "event", fmt.Sprintf("%T", ev))
	return false
}

func (s *Scheduler) dropWorker(chatID string) {
	if entry, ok := s.workers[chatID]; ok {
		delete(s.workers, chatID)
		entry.cancel()
	}
	if cw, ok := s.cronWorkers[chatID]; ok {
		delete(s.cronWorkers, chatID)
		cw.Stop()
	}
}

func (s *Scheduler) getCronWorker(ctx context.Context, chatID string) *CronWorker {
	if cw, ok := s.cronWorkers[chatID]; ok {
		return cw
	}
	w := s.getOrCreate(ctx, chatID)
	cw := NewCronWorker(chatID, w.Events, s.cronLoader)
	s.cronWorkers[chatID] = cw
	return cw
}

func isSlashCommand(msg string) (string, bool) {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return "", false
	}
	return strings.TrimPrefix(msg, "/"), true
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
	}
	return ""
}
