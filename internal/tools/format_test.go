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

func TestFormatTaskSnapshot(t *testing.T) {
	elapsed := 1.5
	normal := tasks.Snapshot{
		TaskID:      "task-1",
		Description: "go test ./...",
		Output:      "ok package\n",
		Status:      tasks.StatusExited,
	}
	errorSnap := tasks.Snapshot{
		TaskID:      "task-2",
		Description: "failing cmd",
		Status:      tasks.StatusFailed,
		Error:       "boom",
	}
	errorSnap.ElapsedSeconds = &elapsed
	noDescription := tasks.Snapshot{
		TaskID: "task-3",
		Status: tasks.StatusRunning,
		Output: "running\n",
	}
	noOutput := tasks.Snapshot{
		TaskID:      "task-4",
		Description: "quiet cmd",
		Status:      tasks.StatusExited,
	}

	tests := []struct {
		name    string
		snap    tasks.Snapshot
		want    []string
		notWant []string
	}{
		{
			name: "normal case",
			snap: normal,
			want: []string{
				"task_id: task-1",
				"description: go test ./...",
				"status: exited",
				"output:\nok package",
			},
			notWant: []string{
				"error:",
				"elapsed_seconds:",
				"description: (none)",
				"output: (no output)",
			},
		},
		{
			name: "error case",
			snap: errorSnap,
			want: []string{
				"task_id: task-2",
				"description: failing cmd",
				"status: failed",
				"error: boom",
				"elapsed_seconds: 1.500",
			},
		},
		{
			name: "empty description",
			snap: noDescription,
			want: []string{
				"task_id: task-3",
				"description: (none)",
				"status: running",
				"output:\nrunning",
			},
			notWant: []string{"description: \n"},
		},
		{
			name: "empty output",
			snap: noOutput,
			want: []string{
				"task_id: task-4",
				"description: quiet cmd",
				"status: exited",
				"output: (no output)",
			},
			notWant: []string{"output:\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTaskSnapshot(tt.snap)
			for _, part := range tt.want {
				if !strings.Contains(got, part) {
					t.Fatalf("expected formatted output to contain %q, got:\n%s", part, got)
				}
			}
			for _, part := range tt.notWant {
				if strings.Contains(got, part) {
					t.Fatalf("expected formatted output to NOT contain %q, got:\n%s", part, got)
				}
			}
		})
	}
}

func TestFormatReadFileResultEmptyContent(t *testing.T) {
	got := formatReadFileResult("internal/empty.go", runtime.ReadFileResult{})

	wantParts := []string{
		"filename: internal/empty.go",
		"total_lines: 0",
		"start_line: 0",
		"returned_lines: 0",
		"content:\n",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", part, got)
		}
	}
	if !strings.HasSuffix(got, "content:\n") {
		t.Fatalf("expected output to end with empty content section, got:\n%s", got)
	}
}

func TestFormatSkillResultEmptyFields(t *testing.T) {
	got := formatSkillResult(&Skill{})

	if !strings.Contains(got, "name: \n") {
		t.Fatalf("expected empty name section, got:\n%s", got)
	}
	if !strings.Contains(got, "base_dir: \n") {
		t.Fatalf("expected empty base_dir section, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "instructions:\n") {
		t.Fatalf("expected output to end with empty instructions section, got:\n%s", got)
	}
}

func TestFormatReadFileRangeResult(t *testing.T) {
	got := formatReadFileRangeResult("long.txt", runtime.ReadFileRangeResult{
		Content:       "abc",
		TotalBytes:    10,
		StartByte:     4,
		ReturnedBytes: 3,
		NextByte:      7,
		EndOfFile:     false,
	})
	for _, want := range []string{
		"filename: long.txt",
		"total_bytes: 10",
		"start_byte: 4",
		"returned_bytes: 3",
		"next_byte: 7",
		"end_of_file: false",
		"content:\nabc",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted result missing %q: %q", want, got)
		}
	}
}
