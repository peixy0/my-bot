package runtime

import (
	"bytes"
	"context"
	"fmt"
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

func (r *ContainerRuntime) Execute(ctx context.Context, command string) (ExecResult, error) {
	stdout, stderr, rc, err := r.execute(ctx, nil, "bash", "-l", "-c", command)
	if err != nil {
		return ExecResult{}, err
	}
	stdoutStr := r.Truncate(ctx, string(stdout), r.maxOutputChars)
	stderrStr := r.Truncate(ctx, string(stderr), r.maxOutputChars)
	return ExecResult{Stdout: stdoutStr, Stderr: stderrStr, ReturnCode: rc}, nil
}

func (r *ContainerRuntime) execute(ctx context.Context, stdin []byte, command ...string) (stdout, stderr []byte, rc int, err error) {
	args := r.buildExecArgs(stdin != nil, command...)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err = cmd.Run()
	rc = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			return nil, nil, 0, err
		}
	}
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), rc, nil
}

func (r *ContainerRuntime) Spawn(ctx context.Context, command string) (*ProcessHandle, error) {
	args := r.buildExecArgs(false, "bash", "-l", "-c", command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
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
		Stdout: stdout,
		Stderr: stderr,
		fnWait: cmd.Wait,
		fnTerminate: func() error {
			return cmd.Process.Signal(syscall.SIGTERM)
		},
		fnKill: func() error {
			return cmd.Process.Kill()
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
	stdout, stderr, rc, err := r.execute(ctx, nil, "cat", "--", filename)
	if err != nil {
		return nil, err
	}
	if rc != 0 {
		return nil, fmt.Errorf("cat failed: %s", strings.TrimSpace(string(stderr)))
	}
	return stdout, nil
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
	_, stderr, rc, err := r.execute(ctx, nil, "mkdir", "-p", "--", path.Dir(filename))
	if err != nil {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("mkdir failed: %s", strings.TrimSpace(string(stderr)))
	}
	_, stderr, rc, err = r.execute(ctx, []byte(content), "tee", "--", filename)
	if err != nil {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("write failed: %s", strings.TrimSpace(string(stderr)))
	}
	return nil
}

func (r *ContainerRuntime) Glob(ctx context.Context, pattern string) (GlobResult, error) {
	return runPythonGlob(ctx, r, pattern)
}

func (r *ContainerRuntime) EditFile(ctx context.Context, filename string, edits []Edit) error {
	return editFile(ctx, r, filename, edits)
}

func (r *ContainerRuntime) OSInfo(ctx context.Context) (string, error) {
	res, err := r.Execute(ctx, "uname -sm && pwd")
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
