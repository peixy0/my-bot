package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
)

func writeCronFile(t *testing.T, dir, name, cronExpr, body string) {
	t.Helper()
	front := ""
	if cronExpr != "" {
		front = "---\nname: " + strings.TrimSuffix(name, ".md") + "\ncron: " + cronExpr + "\n---\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(front+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCronLoader_LoadJob_ParsesValidEntries(t *testing.T) {
	root := t.TempDir()
	jobDir := filepath.Join(root, "morning")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCronFile(t, jobDir, "001-task.md", "0 9 * * *", "do thing one")
	writeCronFile(t, jobDir, "002-task.md", "30 9 * * *", "do thing two")

	loader := NewCronLoader(root)
	defs, err := loader.LoadJob("morning")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("want 2 defs, got %d", len(defs))
	}
	if defs[0].CronExpr != "0 9 * * *" || defs[0].Prompt != "do thing one" {
		t.Errorf("def[0] mismatch: %+v", defs[0])
	}
}

func TestCronLoader_LoadJob_SkipsEntriesWithoutCronExpr(t *testing.T) {
	root := t.TempDir()
	jobDir := filepath.Join(root, "j")
	_ = os.MkdirAll(jobDir, 0o755)
	writeCronFile(t, jobDir, "no-cron.md", "", "no frontmatter cron field")
	writeCronFile(t, jobDir, "ok.md", "* * * * *", "valid")

	defs, err := NewCronLoader(root).LoadJob("j")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("want 1 def, got %d", len(defs))
	}
}

func TestCronLoader_ListJobs(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		_ = os.MkdirAll(filepath.Join(root, name), 0o755)
	}
	// Plus a non-directory entry that should be ignored.
	_ = os.WriteFile(filepath.Join(root, "stray.txt"), []byte(""), 0o644)

	jobs := NewCronLoader(root).ListJobs()
	if len(jobs) != 3 {
		t.Fatalf("want 3 jobs, got %d: %v", len(jobs), jobs)
	}
}

func TestCronLoader_LoadJob_MissingDir(t *testing.T) {
	loader := NewCronLoader(t.TempDir())
	_, err := loader.LoadJob("does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestCronWorker_LoadReloadsExistingJob(t *testing.T) {
	root := t.TempDir()
	jobDir := filepath.Join(root, "morning")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCronFile(t, jobDir, "task.md", "* * * * *", "old prompt")

	workerBox := inbox.NewMemory[events.WorkerEvent](4)
	worker := NewCronWorker("chat-1", workerBox, NewCronLoader(root))
	defer worker.Stop()

	first, err := worker.Load("morning", &captureOutbound{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(worker.scheduler.Entries()) != 1 {
		t.Fatalf("expected one scheduled entry, got defs=%d entries=%d", len(first), len(worker.scheduler.Entries()))
	}

	if err := os.WriteFile(filepath.Join(jobDir, "task.md"), []byte("---\nname: task\ncron: */5 * * * *\n---\nnew prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := worker.Load("morning", &captureOutbound{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("expected one reloaded def, got %d", len(second))
	}
	if len(worker.scheduler.Entries()) != 1 {
		t.Fatalf("expected reload to replace the old entry, got %d entries", len(worker.scheduler.Entries()))
	}
	if len(worker.jobs["morning"]) != 1 {
		t.Fatalf("expected one tracked job entry, got %d", len(worker.jobs["morning"]))
	}
}

func TestCronWorker_LoadReturnsOnlyScheduledEntries(t *testing.T) {
	root := t.TempDir()
	jobDir := filepath.Join(root, "job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCronFile(t, jobDir, "bad.md", "not-a-cron", "bad")
	writeCronFile(t, jobDir, "good.md", "* * * * *", "good")

	worker := NewCronWorker("chat-1", inbox.NewMemory[events.WorkerEvent](4), NewCronLoader(root))
	defer worker.Stop()

	defs, err := worker.Load("job", &captureOutbound{})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected one scheduled entry, got %d", len(defs))
	}
	if defs[0].TaskName != "good" {
		t.Fatalf("expected only good task to be returned, got %+v", defs[0])
	}
}
