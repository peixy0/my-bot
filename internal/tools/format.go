package tools

import (
	"fmt"
	"strings"

	"my-bot/internal/runtime"
)

func formatReadFileResult(filename string, res runtime.ReadFileResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "filename: %s\n", filename)
	fmt.Fprintf(&sb, "total_lines: %d\n", res.TotalLines)
	fmt.Fprintf(&sb, "start_line: %d\n", res.StartLine)
	fmt.Fprintf(&sb, "returned_lines: %d\n", res.ReturnedLines)
	fmt.Fprintf(&sb, "content:\n%s", res.Content)
	return sb.String()
}

func formatSkillResult(skill *Skill) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "name: %s\n", skill.Name)
	fmt.Fprintf(&sb, "base_dir: %s\n", skill.Dir)
	fmt.Fprintf(&sb, "instructions:\n%s", skill.Instructions)
	return sb.String()
}

func formatTaskSnapshot(snap TaskSnapshot) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "task_id: %s\n", snap.TaskID)
	fmt.Fprintf(&sb, "command: %s\n", snap.Command)
	fmt.Fprintf(&sb, "status: %s\n", snap.Status.String())
	if snap.ExitCode != nil {
		fmt.Fprintf(&sb, "exit_code: %d\n", *snap.ExitCode)
	}
	if snap.ElapsedSeconds != nil {
		fmt.Fprintf(&sb, "elapsed_seconds: %.3f\n", *snap.ElapsedSeconds)
	}
	if snap.Stderr != "" {
		fmt.Fprintf(&sb, "stderr:\n%s", snap.Stderr)
	}
	if snap.Stdout != "" {
		fmt.Fprintf(&sb, "stdout:\n%s\n", snap.Stdout)
	} else {
		fmt.Fprintf(&sb, "stdout: (no output)\n")
	}
	return sb.String()
}

func formatRunCommandResult(snap TaskSnapshot) string {
	if snap.Status == taskRunning {
		return fmt.Sprintf(
			"status: running\ntask_id: %s\ncommand still running; use await_task to check",
			snap.TaskID,
		)
	}
	var sb strings.Builder
	if snap.ExitCode != nil {
		fmt.Fprintf(&sb, "exit_code: %d\n", *snap.ExitCode)
	}
	if snap.Stderr != "" {
		fmt.Fprintf(&sb, "stdout:\n%s\n", snap.Stdout)
		fmt.Fprintf(&sb, "stderr:\n%s", snap.Stderr)
	} else {
		fmt.Fprintf(&sb, "%s", snap.Stdout)
	}
	result := sb.String()
	if result == "" {
		return "(no output)"
	}
	return result
}
