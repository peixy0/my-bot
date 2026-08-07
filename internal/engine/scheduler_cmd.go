package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"my-bot/internal/events"
)

var ErrReboot = errors.New("reboot requested")

func (s *Scheduler) handleSlashCommand(ctx context.Context, cmd string, e events.TextInputEvent) error {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}
	switch parts[0] {
	case "heartbeat":
		interval, ok := s.handleHeartbeatInterval(ctx, parts[1:], e.Sender)
		if !ok {
			return nil
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
	case "model", "vision", "temperature", "top_p", "top_k", "max_tokens", "context_window":
		s.handleConfigCommand(ctx, parts, e)
	case "models":
		s.handleModelsCommand(ctx, e)
	case "queue":
		text := strings.TrimSpace(strings.TrimPrefix(cmd, "queue"))
		if text == "" {
			e.Sender.Send(ctx, "usage: /queue <message>")
			return nil
		}
		s.dispatchToSession(ctx, e.ChatID, events.QueuedInputEvent{
			ChatID:    e.ChatID,
			MessageID: e.MessageID,
			Message:   text,
			Sender:    e.Sender,
		})
	case "reboot":
		return s.handleRebootCommand(ctx, e)
	case "dump":
		session, ok := s.sessions[e.ChatID]
		if !ok {
			e.Sender.Send(ctx, "no active session")
			return nil
		}
		go func() {
			id := uuid.NewString()
			if err := session.snapshot(ctx, id); err != nil {
				e.Sender.Send(ctx, fmt.Sprintf("session dump error: %v", err))
				return
			}
			e.Sender.Send(ctx, fmt.Sprintf("session dumped, load with: /resume %s", id))
		}()
	case "resume":
		if len(parts) != 2 {
			e.Sender.Send(ctx, "usage: /resume <id>")
			return nil
		}
		id := parts[1]
		if _, err := uuid.Parse(id); err != nil {
			e.Sender.Send(ctx, "resume id must be a UUID")
			return nil
		}
		go func() {
			if err := s.getOrCreateSession(ctx, e.ChatID).restore(ctx, id); err != nil {
				e.Sender.Send(ctx, fmt.Sprintf("session restore error: %v", err))
				return
			}
			e.Sender.Send(ctx, fmt.Sprintf("session resumed: %s", id))
		}()
	case "compress":
		s.getOrCreateSession(ctx, e.ChatID).publishEvent(events.CompressCommand{Sender: e.Sender})
	case "session":
		e.Sender.Send(ctx, fmt.Sprintf("current session: %s", e.ChatID))
		return nil
	case "abort":
		if !s.getOrCreateSession(ctx, e.ChatID).tryAbort() {
			e.Sender.Send(ctx, "no active conversation")
		}
	case "cron":
		s.handleCronCommand(ctx, parts[1:], e)
	default:
		s.dispatchUserInput(ctx, e.ChatID, e)
	}
	return nil
}

func (s *Scheduler) handleConfigCommand(ctx context.Context, parts []string, e events.TextInputEvent) {
	key := parts[0]
	if len(parts) < 2 {
		s.dispatchToSession(ctx, e.ChatID, events.ConfigQueryEvent{ChatID: e.ChatID, Key: key, Sender: e.Sender})
		return
	}
	s.dispatchToSession(ctx, e.ChatID, events.ConfigChangeEvent{ChatID: e.ChatID, Key: key, Value: parts[1], Sender: e.Sender})
}

func (s *Scheduler) handleModelsCommand(ctx context.Context, e events.TextInputEvent) {
	models, err := s.env.Agent.Models(ctx)
	if err != nil {
		e.Sender.Send(ctx, fmt.Sprintf("error listing models: %v", err))
		return
	}
	if len(models) == 0 {
		e.Sender.Send(ctx, "no models available")
		return
	}
	e.Sender.Send(ctx, "available models:\n- "+strings.Join(models, "\n- "))
}

func (s *Scheduler) stopAndDumpAllSessions(ctx context.Context) ([]restoreSession, []error) {
	restores := make([]restoreSession, 0, len(s.sessions))
	var failures []error
	for chatID, session := range s.sessions {
		dumpID := uuid.NewString()
		if err := session.stopAndSnapshot(ctx, dumpID); err != nil {
			failures = append(failures, fmt.Errorf("dump %s: %w", chatID, err))
			continue
		}
		restores = append(restores, restoreSession{ChatID: chatID, DumpID: dumpID})
	}
	return restores, failures
}

func (s *Scheduler) handleRebootCommand(ctx context.Context, e events.TextInputEvent) error {
	if len(s.sessions) == 0 {
		e.Sender.Send(ctx, fmt.Sprintf("rebooting"))
		return ErrReboot
	}
	total := len(s.sessions)
	restores, failures := s.stopAndDumpAllSessions(ctx)
	if err := s.writeCheckpoint(restores); err != nil {
		failures = append(failures, fmt.Errorf("write checkpoint: %w", err))
	}
	for _, err := range failures {
		slog.Error("reboot snapshot failed", "err", err)
	}
	if len(failures) == 0 {
		e.Sender.Send(ctx, fmt.Sprintf("dumped %d session(s); rebooting", len(restores)))
		return ErrReboot
	}
	e.Sender.Send(ctx, fmt.Sprintf("dumped %d/%d session(s); rebooting with %d error(s)", len(restores), total, len(failures)))
	return ErrReboot
}

func (s *Scheduler) handleHeartbeatInterval(ctx context.Context, args []string, sender events.Outbound) (int, bool) {
	if len(args) == 0 {
		return s.env.Cfg.Heartbeat.IntervalSeconds, true
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

func isSlashCommand(msg string) (string, bool) {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return "", false
	}
	return strings.TrimPrefix(msg, "/"), true
}
