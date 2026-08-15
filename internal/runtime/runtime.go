package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

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

type ReadFileRangeResult struct {
	Content       string
	TotalBytes    int64
	StartByte     int64
	ReturnedBytes int
	NextByte      int64
	EndOfFile     bool
}

type Edit struct {
	Search  string
	Replace string
}

type GlobResult struct {
	Items        []string `json:"items"`
	Count        int      `json:"count"`
	ExceedsLimit bool     `json:"exceeds_limit,omitempty"`
}

type ProcessHandle struct {
	PID         int
	Stdin       io.WriteCloser
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

func newProcessHandle(cmd *exec.Cmd, stdin io.WriteCloser, stdout, stderr io.ReadCloser) *ProcessHandle {
	return &ProcessHandle{
		PID:    cmd.Process.Pid,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		fnWait: cmd.Wait,
		fnTerminate: func() error {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		},
		fnKill: func() error {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		},
		fnExitCode: func() *int {
			if cmd.ProcessState == nil {
				return nil
			}
			rc := cmd.ProcessState.ExitCode()
			return &rc
		},
	}
}

type Executor interface {
	ExecuteTruncated(ctx context.Context, stdin io.Reader, command ...string) (ExecResult, error)
	Execute(ctx context.Context, stdin io.Reader, command ...string) (ExecResult, error)
	Spawn(ctx context.Context, command string) (*ProcessHandle, error)
}

type FileSystem interface {
	ReadRawBytes(ctx context.Context, filename string) ([]byte, error)
	ReadFile(ctx context.Context, filename string, startLine, limit int) (ReadFileResult, error)
	ReadFileRange(ctx context.Context, filename string, startByte int64, limit int) (ReadFileRangeResult, error)
	WriteFile(ctx context.Context, filename, content string) error
	WriteTmpFile(ctx context.Context, content string) (string, error)
	AppendFile(ctx context.Context, filename, content string) error
	EditFile(ctx context.Context, filename string, edits []Edit) error
}

type Globber interface {
	Glob(ctx context.Context, pattern string, limit int) (GlobResult, error)
}

type Truncater interface {
	Truncate(ctx context.Context, text string, limit int) string
	TruncateTail(ctx context.Context, text string, limit int) string
}

type OSInfoProvider interface {
	OSInfo(ctx context.Context) (string, error)
}

type Runtime interface {
	Executor
	FileSystem
	Globber
	Truncater
	OSInfoProvider
}

func truncate(text string, maxChars int) (string, bool) {
	if len(text) <= maxChars {
		return text, false
	}
	return text[:maxChars], true
}

func truncateWithRedirection(
	ctx context.Context,
	rt FileSystem,
	text string,
	maxChars int,
) string {
	short, cut := truncate(text, maxChars)
	if !cut {
		return text
	}
	path, err := writeTmpFile(ctx, rt, text)
	if err != nil {
		return fmt.Sprintf("[output truncated; showing the first %d chars]\n\n%s", len(short), short)
	}
	return fmt.Sprintf("[output truncated; showing the first %d chars; full output saved to %s]\n\n%s", len(short), path, short)
}

func truncateTail(text string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return "", text != ""
	}
	if len(text) <= maxChars {
		return text, false
	}
	return text[len(text)-maxChars:], true
}

func truncateTailWithRedirection(
	ctx context.Context,
	rt FileSystem,
	text string,
	maxChars int,
) string {
	short, cut := truncateTail(text, maxChars)
	if !cut {
		return text
	}
	path, err := writeTmpFile(ctx, rt, text)
	if err != nil {
		return fmt.Sprintf("[output truncated; showing the last %d chars]\n\n%s", len(short), short)
	}
	return fmt.Sprintf("[output truncated; showing the last %d chars; full output saved to %s]\n\n%s", len(short), path, short)
}

func writeTmpFile(ctx context.Context, rt FileSystem, content string) (string, error) {
	path := fmt.Sprintf("./tmp/output-%s", uuid.NewString())
	err := rt.WriteFile(ctx, path, content)
	return path, err
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

func runPythonGlob(ctx context.Context, rt Executor, pattern string, limit int) (GlobResult, error) {
	const pyScript = `import glob, json, sys
p = sys.argv[1]
limit = int(sys.argv[2])
items = sorted(glob.glob(p, recursive=True))
exceeds = len(items) > limit
items = items[:limit]
print(json.dumps({'items': items, 'count': len(items), 'exceeds_limit': exceeds}))`
	res, err := rt.Execute(ctx, nil, "python3", "-c", pyScript, pattern, strconv.Itoa(limit))
	if err != nil {
		return GlobResult{}, err
	}
	if res.ReturnCode != 0 {
		return GlobResult{}, fmt.Errorf("glob failed: %s", strings.TrimSpace(res.Stderr))
	}
	var parsed GlobResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &parsed); err != nil {
		return GlobResult{}, fmt.Errorf("glob parse failed: %w: %q", err, res.Stdout)
	}
	return parsed, nil
}

func editFile(ctx context.Context, rt FileSystem, filename string, edits []Edit) error {
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
