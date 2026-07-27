package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
)

type Scheduler struct {
	env        SessionEnv
	inbox      inbox.Inbox[events.AgentEvent]
	cronLoader *CronLoader

	sessions map[string]*chatSession
}

func NewScheduler(
	env SessionEnv,
	agentInbox inbox.Inbox[events.AgentEvent],
	cronLoader *CronLoader,
) *Scheduler {
	return &Scheduler{
		env:        env,
		inbox:      agentInbox,
		cronLoader: cronLoader,
		sessions:   make(map[string]*chatSession),
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	defer s.closeAllSessions()
	s.maybeRestoreSessions(ctx)
	for {
		msg, err := s.inbox.Receive(ctx)
		if err != nil {
			return err
		}
		if err := s.dispatch(ctx, msg); err != nil {
			return err
		}
	}
}

func (s *Scheduler) dispatch(ctx context.Context, ev events.AgentEvent) error {
	switch e := ev.(type) {
	case events.TextInputEvent:
		if cmd, ok := isSlashCommand(e.Message); ok {
			return s.handleSlashCommand(ctx, cmd, e)
		}
		s.dispatchUserInput(ctx, e.ChatID, e)
	case events.ImageInputEvent:
		s.dispatchUserInput(ctx, e.ChatID, e)
	case events.DropSessionEvent:
		s.closeSession(e.ChatID)
	}
	return nil
}

func (s *Scheduler) dispatchUserInput(ctx context.Context, chatID string, e events.MessageEvent) {
	session := s.getOrCreateSession(ctx, chatID)
	session.publishMessage(e)
}

func (s *Scheduler) getOrCreateSession(ctx context.Context, chatID string) *chatSession {
	if session, ok := s.sessions[chatID]; ok {
		return session
	}
	sessionCfg := s.env.Cfg.ForSession(chatID)
	sessionEnv := SessionEnv{
		Cfg:           sessionCfg,
		Rt:            s.env.Rt,
		Agent:         s.env.Agent,
		Skills:        s.env.Skills,
		BrowserBroker: s.env.BrowserBroker,
	}
	session := newChatSession(ctx, chatID, sessionEnv, s.cronLoader)
	s.sessions[chatID] = session
	return session
}

func (s *Scheduler) dispatchToSession(ctx context.Context, chatID string, ev events.WorkerEvent) bool {
	if chatID == "" {
		slog.Error("worker event dropped: missing chat id", "event", fmt.Sprintf("%T", ev))
		return false
	}
	return s.getOrCreateSession(ctx, chatID).publishEvent(ev)
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
	sessions := make([]*chatSession, 0, len(s.sessions))
	for chatID, session := range s.sessions {
		delete(s.sessions, chatID)
		session.close()
		sessions = append(sessions, session)
	}
	for _, session := range sessions {
		if session.done != nil {
			<-session.done
		}
	}
}

type restoreSession struct {
	ChatID string `json:"chat_id"`
	DumpID string `json:"dump_id"`
}

func (s *Scheduler) restorePath() string {
	return filepath.Join(s.env.Cfg.Workspace.SessionDir, ".checkpoint")
}

func (s *Scheduler) writeCheckpoint(sessions []restoreSession) error {
	data, err := json.Marshal(sessions)
	if err != nil {
		return fmt.Errorf("marshal restore marker: %w", err)
	}
	dir := filepath.Dir(s.restorePath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".checkpoint-*")
	if err != nil {
		return fmt.Errorf("create restore marker: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return fmt.Errorf("chmod restore marker: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write restore marker: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close restore marker: %w", err)
	}
	if err := os.Rename(tempPath, s.restorePath()); err != nil {
		return fmt.Errorf("replace restore marker: %w", err)
	}
	return nil
}

func (s *Scheduler) maybeRestoreSessions(ctx context.Context) {
	data, err := os.ReadFile(s.restorePath())
	keepCheckpoint := false
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		slog.Error("fail to read restore marker", "err", err)
		return
	}
	var sessions []restoreSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		slog.Error("fail to unmarshal restore marker", "err", err)
		return
	}
	for _, entry := range sessions {
		if entry.ChatID == "" || entry.DumpID == "" {
			keepCheckpoint = true
			slog.Warn("session checkpoint contains empty session")
			continue
		}
		if err := s.getOrCreateSession(ctx, entry.ChatID).restore(ctx, entry.DumpID); err != nil {
			keepCheckpoint = true
			slog.Error("fail to restore session", "chat_id", entry.ChatID, "err", err)
			continue
		}
		slog.Info("session restored", "chat_id", entry.ChatID, "dump_id", entry.DumpID)
	}
	if keepCheckpoint {
		return
	}
	if err := os.Remove(s.restorePath()); err != nil {
		slog.Warn("fail to remove session checkpoint", "err", err)
	}
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
