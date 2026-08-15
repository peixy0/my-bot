package tools

import (
	"fmt"
	"strings"

	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
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

func formatReadFileRangeResult(filename string, res runtime.ReadFileRangeResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "filename: %s\n", filename)
	fmt.Fprintf(&sb, "total_bytes: %d\n", res.TotalBytes)
	fmt.Fprintf(&sb, "start_byte: %d\n", res.StartByte)
	fmt.Fprintf(&sb, "returned_bytes: %d\n", res.ReturnedBytes)
	fmt.Fprintf(&sb, "next_byte: %d\n", res.NextByte)
	fmt.Fprintf(&sb, "end_of_file: %t\n", res.EndOfFile)
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

func formatTaskSnapshot(snap tasks.Snapshot) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "task_id: %s\n", snap.TaskID)
	if snap.Description != "" {
		fmt.Fprintf(&sb, "description: %s\n", snap.Description)
	} else {
		fmt.Fprintf(&sb, "description: (none)\n")
	}
	fmt.Fprintf(&sb, "status: %s\n", snap.Status)
	if snap.Error != "" {
		fmt.Fprintf(&sb, "error: %s\n", snap.Error)
	}
	if snap.ElapsedSeconds != nil {
		fmt.Fprintf(&sb, "elapsed_seconds: %.3f\n", *snap.ElapsedSeconds)
	}
	if snap.Output != "" {
		fmt.Fprintf(&sb, "output:\n%s\n", snap.Output)
	} else {
		fmt.Fprintf(&sb, "output: (no output)\n")
	}
	return sb.String()
}
