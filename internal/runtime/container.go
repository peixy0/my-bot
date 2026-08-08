package runtime

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"
	"syscall"
)

type ContainerRuntime struct {
	maxOutputChars int
	containerName  string
	runtimeBin     string
	workdir        string
}

func NewContainerRuntime(containerName, runtimeBin, workdir string, maxOutputChars int) (*ContainerRuntime, error) {
	if _, err := exec.LookPath(runtimeBin); err != nil {
		return nil, fmt.Errorf("container runtime %q not found in PATH: %w", runtimeBin, err)
	}
	return &ContainerRuntime{
		maxOutputChars: maxOutputChars,
		containerName:  containerName,
		runtimeBin:     runtimeBin,
		workdir:        workdir,
	}, nil
}

func (r *ContainerRuntime) Truncate(ctx context.Context, text string, limit int) string {
	return truncateWithRedirection(ctx, r, text, limit)
}

func (r *ContainerRuntime) TruncateTail(ctx context.Context, text string, limit int) string {
	return truncateTailWithRedirection(ctx, r, text, limit)
}

func (r *ContainerRuntime) buildExecArgs(withStdin bool, command ...string) []string {
	args := []string{r.runtimeBin, "exec"}
	if withStdin {
		args = append(args, "-i")
	}
	args = append(args,
		"-w", r.workdir,
		r.containerName,
	)
	args = append(args, command...)
	return args
}

func (r *ContainerRuntime) ExecuteTruncated(ctx context.Context, stdin io.Reader, command ...string) (ExecResult, error) {
	cmdArgs := append([]string{"bash", "-l", "-c"}, command...)
	result, err := r.Execute(ctx, stdin, cmdArgs...)
	if err != nil {
		return ExecResult{}, err
	}
	stdoutStr := r.Truncate(ctx, result.Stdout, r.maxOutputChars)
	stderrStr := r.Truncate(ctx, result.Stderr, r.maxOutputChars)
	return ExecResult{Stdout: stdoutStr, Stderr: stderrStr, ReturnCode: result.ReturnCode}, nil
}

func (r *ContainerRuntime) Execute(ctx context.Context, stdin io.Reader, command ...string) (ExecResult, error) {
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

func (r *ContainerRuntime) Spawn(ctx context.Context, command string) (*ProcessHandle, error) {
	args := r.buildExecArgs(true, "bash", "-l", "-c", command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
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

func (r *ContainerRuntime) ReadRawBytes(ctx context.Context, filename string) ([]byte, error) {
	res, err := r.Execute(ctx, nil, "cat", "--", filename)
	if err != nil {
		return nil, err
	}
	if res.ReturnCode != 0 {
		return nil, fmt.Errorf("cat failed: %s", strings.TrimSpace(res.Stderr))
	}
	return []byte(res.Stdout), nil
}

func (r *ContainerRuntime) ReadFile(ctx context.Context, filename string, startLine, limit int) (ReadFileResult, error) {
	data, err := r.ReadRawBytes(ctx, filename)
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

func (r *ContainerRuntime) WriteFile(ctx context.Context, filename, content string) error {
	res, err := r.Execute(ctx, nil, "mkdir", "-p", "--", path.Dir(filename))
	if err != nil {
		return err
	}
	if res.ReturnCode != 0 {
		return fmt.Errorf("mkdir failed: %s", strings.TrimSpace(res.Stderr))
	}
	res, err = r.Execute(ctx, strings.NewReader(content), "tee", "--", filename)
	if err != nil {
		return err
	}
	if res.ReturnCode != 0 {
		return fmt.Errorf("write failed: %s", strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (r *ContainerRuntime) WriteTmpFile(ctx context.Context, content string) (string, error) {
	return writeTmpFile(ctx, r, content)
}

func (r *ContainerRuntime) AppendFile(ctx context.Context, filename, content string) error {
	res, err := r.Execute(ctx, nil, "mkdir", "-p", "--", path.Dir(filename))
	if err != nil {
		return err
	}
	if res.ReturnCode != 0 {
		return fmt.Errorf("mkdir failed: %s", strings.TrimSpace(res.Stderr))
	}
	res, err = r.Execute(ctx, strings.NewReader(content), "tee", "-a", "--", filename)
	if err != nil {
		return err
	}
	if res.ReturnCode != 0 {
		return fmt.Errorf("append failed: %s", strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (r *ContainerRuntime) Glob(ctx context.Context, pattern string, limit int) (GlobResult, error) {
	return runPythonGlob(ctx, r, pattern, limit)
}

func (r *ContainerRuntime) EditFile(ctx context.Context, filename string, edits []Edit) error {
	return editFile(ctx, r, filename, edits)
}

func (r *ContainerRuntime) OSInfo(ctx context.Context) (string, error) {
	res, err := r.Execute(ctx, nil, "bash", "-l", "-c", "uname -sm && pwd")
	if err != nil {
		return "", err
	}
	if res.ReturnCode != 0 {
		return "", fmt.Errorf("osinfo failed: %s", strings.TrimSpace(res.Stderr))
	}
	lines := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)
	osLine := strings.TrimSpace(lines[0])
	cwd := "unknown"
	if len(lines) > 1 {
		cwd = strings.TrimSpace(lines[1])
	}
	return fmt.Sprintf("OS: %s\nWorking directory: %s", osLine, cwd), nil
}
