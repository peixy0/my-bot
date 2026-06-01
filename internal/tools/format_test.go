package tools

import (
	"strings"
	"testing"

	"my-bot/internal/runtime"
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
	exitCode := 2
	got := formatTaskSnapshot(TaskSnapshot{
		TaskID:   "task-1",
		Command:  "go test ./...",
		Stdout:   "ok package\n",
		Stderr:   "failed package\n",
		Status:   taskExited,
		ExitCode: &exitCode,
	})

	wantParts := []string{
		"task_id: task-1",
		"command: go test ./...",
		"status: exited",
		"exit_code: 2",
		"stdout:\nok package",
		"stderr:\nfailed package",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", part, got)
		}
	}
}

func TestFormatRunCommandResultRunning(t *testing.T) {
	got := formatRunCommandResult(TaskSnapshot{
		TaskID: "task-2",
		Status: taskRunning,
	})

	if !strings.Contains(got, "status: running") {
		t.Fatalf("expected running status, got:\n%s", got)
	}
	if !strings.Contains(got, "task_id: task-2") {
		t.Fatalf("expected task id, got:\n%s", got)
	}
	if strings.Contains(got, "{") {
		t.Fatalf("expected readable text, got JSON-like output:\n%s", got)
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
