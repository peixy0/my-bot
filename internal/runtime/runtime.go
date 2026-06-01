package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

type ExecResult struct {
	Stdout     string
	Stderr     string
	ReturnCode int
}

type ReadFileResult struct {
	Content       string
	TotalLines    int
	StartLine     int
	ReturnedLines int
}

type Edit struct {
	Search  string
	Replace string
}

type GlobResult struct {
	Items []string `json:"items"`
	Count int      `json:"count"`
}

type ProcessHandle struct {
	PID         int
	Stdout      io.ReadCloser
	Stderr      io.ReadCloser
	fnWait      func() error
	fnTerminate func() error
	fnKill      func() error
	fnExitCode  func() *int
}

func (h *ProcessHandle) Wait() error      { return h.fnWait() }
func (h *ProcessHandle) Terminate() error { return h.fnTerminate() }
func (h *ProcessHandle) Kill() error      { return h.fnKill() }
func (h *ProcessHandle) ExitCode() *int   { return h.fnExitCode() }

type Runtime interface {
	Truncate(ctx context.Context, text string, limit int) string
	Execute(ctx context.Context, command string) (ExecResult, error)
	Spawn(ctx context.Context, command string) (*ProcessHandle, error)
	ReadRawBytes(ctx context.Context, filename string) ([]byte, error)
	ReadFile(ctx context.Context, filename string, startLine, limit int) (ReadFileResult, error)
	WriteFile(ctx context.Context, filename, content string) error
	Glob(ctx context.Context, pattern string) (GlobResult, error)
	EditFile(ctx context.Context, filename string, edits []Edit) error
	OSInfo(ctx context.Context) (string, error)
}

func truncate(text string, maxChars int) (string, bool) {
	if len(text) <= maxChars {
		return text, false
	}
	return text[:maxChars], true
}

func truncateWithRedirection(
	ctx context.Context,
	rt Runtime,
	text string,
	maxChars int,
) string {
	short, cut := truncate(text, maxChars)
	if !cut {
		return text
	}
	path := fmt.Sprintf("./tmp/trunc-%s.txt", uuid.NewString())
	err := rt.WriteFile(ctx, path, text)
	if err != nil {
		return fmt.Sprintf("[output truncated; showing the first %d chars]\n\n%s", len(short), short)
	}
	return fmt.Sprintf("[output truncated; showing the first %d chars; full output saved to %s]\n\n%s", len(short), path, short)
}

func truncateLinesWithNote(
	lines []string,
	total int,
	start int,
	limit int,
	maxChars int,
) (string, int) {
	if start >= total {
		return "", 0
	}
	end := start + limit
	if end > total {
		end = total
	}
	content := lines[start]
	cursor := start + 1
	for {
		if len(content) > maxChars {
			content = fmt.Sprintf("[output truncated; showing the first %d chars]\n\n%s", maxChars, content[:maxChars])
			break
		}
		if cursor >= end {
			break
		}
		content += "\n" + lines[cursor]
		cursor++
	}
	return content, cursor - start
}

func runPythonGlob(ctx context.Context, rt Runtime, pattern string) (GlobResult, error) {
	script := fmt.Sprintf(
		`python3 -c "import glob, json, sys; p=sys.argv[1]; items=sorted(glob.glob(p, recursive=True)); print(json.dumps({'items': items, 'count': len(items)}))" %q`,
		pattern,
	)
	res, err := rt.Execute(ctx, script)
	if err != nil {
		return GlobResult{}, err
	}
	if res.ReturnCode != 0 {
		return GlobResult{}, fmt.Errorf("glob failed: %s", strings.TrimSpace(res.Stderr))
	}
	var parsed GlobResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &parsed); err != nil {
		return GlobResult{}, fmt.Errorf("glob parse failed: %w", err)
	}
	if parsed.Items == nil {
		parsed.Items = []string{}
	}
	if parsed.Count == 0 {
		parsed.Count = len(parsed.Items)
	}
	return parsed, nil
}

func editFile(ctx context.Context, rt Runtime, filename string, edits []Edit) error {
	data, err := rt.ReadRawBytes(ctx, filename)
	if err != nil {
		return err
	}
	content := string(data)
	for _, e := range edits {
		matches := strings.Count(content, e.Search)
		if matches == 0 {
			return fmt.Errorf("search text not found, use `read_file` or `grep` to confirm the exact content")
		}
		if matches > 1 {
			return fmt.Errorf("search text (%s) is ambiguous (%d matches)", e.Search, matches)
		}
		content = strings.Replace(content, e.Search, e.Replace, 1)
	}
	return rt.WriteFile(ctx, filename, content)
}
