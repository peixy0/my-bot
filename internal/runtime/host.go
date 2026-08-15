package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

type HostRuntime struct {
	maxOutputChars int
}

func NewHostRuntime(maxOutputChars int) *HostRuntime {
	return &HostRuntime{maxOutputChars: maxOutputChars}
}

func (r *HostRuntime) Truncate(ctx context.Context, text string, limit int) string {
	return truncateWithRedirection(ctx, r, text, limit)
}

func (r *HostRuntime) TruncateTail(ctx context.Context, text string, limit int) string {
	return truncateTailWithRedirection(ctx, r, text, limit)
}

func (r *HostRuntime) ExecuteTruncated(ctx context.Context, stdin io.Reader, command ...string) (ExecResult, error) {
	result, err := r.Execute(ctx, stdin, command...)
	if err != nil {
		return ExecResult{}, err
	}
	stdoutStr := r.Truncate(ctx, result.Stdout, r.maxOutputChars)
	stderrStr := r.Truncate(ctx, result.Stderr, r.maxOutputChars)
	return ExecResult{Stdout: stdoutStr, Stderr: stderrStr, ReturnCode: result.ReturnCode}, nil
}

func (r *HostRuntime) Execute(ctx context.Context, stdin io.Reader, command ...string) (ExecResult, error) {
	if len(command) == 0 {
		return ExecResult{}, fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = stdin
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ReturnCode: exitErr.ExitCode()}, nil
		}
		return ExecResult{}, err
	}
	return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ReturnCode: 0}, nil
}

func (r *HostRuntime) Spawn(ctx context.Context, command string) (*ProcessHandle, error) {
	cmd := exec.CommandContext(ctx, "bash", "-l", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return newProcessHandle(cmd, stdin, stdout, stderr), nil
}

func (r *HostRuntime) ReadRawBytes(ctx context.Context, filename string) ([]byte, error) {
	return os.ReadFile(filename)
}

func (r *HostRuntime) ReadFile(ctx context.Context, filename string, startLine, limit int) (ReadFileResult, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return ReadFileResult{}, err
	}
	lines := strings.Split(string(data), "\n")
	if startLine < 1 {
		startLine = 1
	}
	total := len(lines)
	start := startLine - 1
	content, numLines := truncateLinesWithNote(lines, total, start, limit, r.maxOutputChars)
	return ReadFileResult{
		Content:       content,
		TotalLines:    total,
		StartLine:     startLine,
		ReturnedLines: numLines,
	}, nil
}

func (r *HostRuntime) ReadFileRange(ctx context.Context, filename string, startByte int64, limit int) (ReadFileRangeResult, error) {
	file, err := os.Open(filename)
	if err != nil {
		return ReadFileRangeResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ReadFileRangeResult{}, err
	}
	if startByte < 0 {
		startByte = 0
	}
	if startByte > info.Size() {
		startByte = info.Size()
	}
	if _, err := file.Seek(startByte, io.SeekStart); err != nil {
		return ReadFileRangeResult{}, err
	}
	if limit < 0 {
		limit = 0
	}
	data := make([]byte, limit)
	n, err := io.ReadFull(file, data)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return ReadFileRangeResult{}, err
	}
	data = data[:n]
	next := startByte + int64(n)
	return ReadFileRangeResult{Content: string(data), TotalBytes: info.Size(), StartByte: startByte, ReturnedBytes: n, NextByte: next, EndOfFile: next >= info.Size()}, nil
}

func (r *HostRuntime) WriteFile(ctx context.Context, filename, content string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	return os.WriteFile(filename, []byte(content), 0644)
}

func (r *HostRuntime) WriteTmpFile(ctx context.Context, content string) (string, error) {
	return writeTmpFile(ctx, r, content)
}

func (r *HostRuntime) AppendFile(ctx context.Context, filename, content string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return err
	}
	return nil
}

func (r *HostRuntime) Glob(ctx context.Context, pattern string, limit int) (GlobResult, error) {
	return runPythonGlob(ctx, r, pattern, limit)
}

func (r *HostRuntime) EditFile(ctx context.Context, filename string, edits []Edit) error {
	return editFile(ctx, r, filename, edits)
}

func (r *HostRuntime) OSInfo(_ context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}
	return fmt.Sprintf("OS: %s/%s\nWorking directory: %s", runtime.GOOS, runtime.GOARCH, filepath.ToSlash(cwd)), nil
}
