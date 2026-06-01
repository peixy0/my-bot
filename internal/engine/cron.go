package engine

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/robfig/cron/v3"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/tools"
)

type CronJobDef struct {
	TaskName string
	CronExpr string
	Prompt   string
}

type CronLoader struct {
	cronsDir string
}

func NewCronLoader(cronsDir string) *CronLoader {
	return &CronLoader{cronsDir: cronsDir}
}

func (l *CronLoader) ListJobs() []string {
	entries, err := os.ReadDir(l.cronsDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func (l *CronLoader) LoadJob(name string) ([]CronJobDef, error) {
	dir := filepath.Join(l.cronsDir, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cron job %q: %w", name, err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)

	var defs []CronJobDef
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			slog.Warn("cron: skipping unreadable file", "file", f, "err", err)
			continue
		}
		fm, body := tools.ParseFrontmatter(string(data))
		def := CronJobDef{
			TaskName: fm["name"],
			CronExpr: fm["cron"],
			Prompt:   strings.TrimSpace(body),
		}
		if def.TaskName == "" {
			def.TaskName = strings.TrimSuffix(filepath.Base(f), ".md")
		}
		if def.CronExpr != "" {
			defs = append(defs, def)
		}
	}
	return defs, nil
}

type CronWorker struct {
	chatID    string
	workerBox inbox.Inbox[events.WorkerEvent]
	loader    *CronLoader
	scheduler *cron.Cron
	jobs      map[string][]cron.EntryID
}

func NewCronWorker(chatID string, workerBox inbox.Inbox[events.WorkerEvent], loader *CronLoader) *CronWorker {
	return &CronWorker{
		chatID:    chatID,
		workerBox: workerBox,
		loader:    loader,
		scheduler: cron.New(),
		jobs:      make(map[string][]cron.EntryID),
	}
}

func (cw *CronWorker) Load(jobName string, sender events.Outbound) ([]CronJobDef, error) {
	cw.Unload(jobName)
	defs, err := cw.loader.LoadJob(jobName)
	if err != nil {
		return nil, err
	}
	var ids []cron.EntryID
	var scheduled []CronJobDef
	for _, def := range defs {
		def := def
		id, err := cw.scheduler.AddFunc(def.CronExpr, func() {
			ev := events.CronEvent{
				ChatID:   cw.chatID,
				TaskName: def.TaskName,
				Prompt:   def.Prompt,
				Sender:   sender,
			}
			if !cw.workerBox.TryPublish(workerEnvelope(cw.chatID, ev)) {
				slog.Warn("cron event dropped: worker inbox full", "chat", cw.chatID, "task", def.TaskName)
			}
		})
		if err != nil {
			slog.Warn("cron: invalid cron expression",
				"job", jobName, "task", def.TaskName, "expr", def.CronExpr, "err", err)
			continue
		}
		ids = append(ids, id)
		scheduled = append(scheduled, def)
	}
	if len(ids) > 0 {
		cw.jobs[jobName] = ids
		cw.scheduler.Start()
	}
	return scheduled, nil
}

func (cw *CronWorker) Unload(jobName string) bool {
	ids, ok := cw.jobs[jobName]
	if !ok {
		return false
	}
	for _, id := range ids {
		cw.scheduler.Remove(id)
	}
	delete(cw.jobs, jobName)
	return true
}

func (cw *CronWorker) UnloadAll() {
	for name := range cw.jobs {
		cw.Unload(name)
	}
}

func (cw *CronWorker) LoadedJobs() []string {
	names := make([]string, 0, len(cw.jobs))
	for name := range cw.jobs {
		names = append(names, name)
	}
	return names
}

func (cw *CronWorker) Stop() {
	cw.scheduler.Stop()
}
