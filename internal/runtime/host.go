package runtime

import (
	"bytes"
	"context"
	"fmt"
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

func (r *HostRuntime) Execute(ctx context.Context, command string) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, "bash", "-l", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rc := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			return ExecResult{}, err
		}
	}
	stdoutStr := r.Truncate(ctx, stdout.String(), r.maxOutputChars)
	stderrStr := r.Truncate(ctx, stderr.String(), r.maxOutputChars)
	return ExecResult{Stdout: stdoutStr, Stderr: stderrStr, ReturnCode: rc}, nil
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

	h := &ProcessHandle{
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
	return h, nil
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

func (r *HostRuntime) Glob(ctx context.Context, pattern string) (GlobResult, error) {
	return runPythonGlob(ctx, r, pattern)
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
