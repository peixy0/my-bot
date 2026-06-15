package tools

import (
	"strings"
	"testing"

	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
)

func TestFormatReadFileResultUsesRawLineText(t *testing.T) {
	got := formatReadFileResult("internal/example.go", runtime.ReadFileResult{
		Content:       "fmt.Println(\"hi\")\npath := `C:\\tmp`",
		TotalLines:    10,
		StartLine:     4,
		ReturnedLines: 2,
	})

	wantParts := []string{
		"filename: internal/example.go",
		"total_lines: 10",
		"start_line: 4",
		"returned_lines: 2",
		"content:\nfmt.Println(\"hi\")",
		"path := `C:\\tmp`",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", part, got)
		}
	}
	if strings.Contains(got, `\"hi\"`) || strings.Contains(got, `\\tmp`) {
		t.Fatalf("expected raw content without JSON escaping, got:\n%s", got)
	}
}

func TestFormatTaskSnapshotUsesReadableOutputSections(t *testing.T) {
	got := formatTaskSnapshot(tasks.Snapshot{
		TaskID:      "task-1",
		Description: "go test ./...",
		Output:      "ok package\nfailed package\nexit_code: 2\n",
		Status:      tasks.StatusExited,
	})

	wantParts := []string{
		"task_id: task-1",
		"description: go test ./...",
		"status: exited",
		"output:\nok package\nfailed package",
		"exit_code: 2",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", part, got)
		}
	}
}

func TestFormatSkillResultUsesRawInstructions(t *testing.T) {
	got := formatSkillResult(&Skill{
		Name:         "demo",
		Dir:          "/tmp/demo",
		Instructions: "Use `foo(\"bar\")`.\nKeep C:\\tmp intact.",
	})

	if strings.Contains(got, `\"bar\"`) || strings.Contains(got, `\\tmp`) {
		t.Fatalf("expected raw instructions without JSON escaping, got:\n%s", got)
	}
	if !strings.Contains(got, "instructions:\nUse `foo(\"bar\")`.") {
		t.Fatalf("expected raw instruction body, got:\n%s", got)
	}
}
