package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHostRuntimeTruncateTailSavesFullOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	rt := NewHostRuntime(1024)
	got := rt.TruncateTail(context.Background(), "hello world", 5)
	if !strings.Contains(got, "[output truncated; showing the last 5 chars; full output saved to ") || !strings.HasSuffix(got, "\n\nworld") {
		t.Fatalf("unexpected truncated output: %q", got)
	}
	entries, err := os.ReadDir("tmp")
	if err != nil {
		t.Fatalf("read tmp: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one saved output, got %d", len(entries))
	}
	full, err := os.ReadFile(filepath.Join("tmp", entries[0].Name()))
	if err != nil {
		t.Fatalf("read full output: %v", err)
	}
	if string(full) != "hello world" {
		t.Fatalf("unexpected full output: %q", full)
	}
}

func TestHostRuntimeGlobUsesPatternDirectly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "c.go"), []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pattern := filepath.Join(root, "**", "*.go")
	got, err := NewHostRuntime(1024).Glob(context.Background(), pattern, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, "a.go"),
		filepath.Join(root, "sub", "c.go"),
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("glob items mismatch:\nwant %#v\ngot  %#v", want, got.Items)
	}
	if got.Count != len(want) {
		t.Fatalf("want count %d, got %d", len(want), got.Count)
	}
}

func TestContainerRuntimeExecutePreservesArguments(t *testing.T) {
	root := t.TempDir()
	runtimeBin := filepath.Join(root, "fake-runtime")
	script := `#!/bin/sh
if [ "$1" != "exec" ]; then
  exit 90
fi
shift
if [ "$1" != "-i" ]; then
  exit 91
fi
shift
if [ "$1" != "-w" ]; then
  exit 92
fi
shift 2
if [ "$1" != "container" ]; then
  exit 93
fi
shift
exec "$@"
`
	if err := os.WriteFile(runtimeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	rt := &ContainerRuntime{
		maxOutputChars: 1024,
		containerName:  "container",
		runtimeBin:     runtimeBin,
		workdir:        "/workspace",
	}
	got, err := rt.Execute(context.Background(), nil, "printf", "%s", "a b;printf bad >&2")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReturnCode != 0 {
		t.Fatalf("return code = %d, stderr = %q", got.ReturnCode, got.Stderr)
	}
	if got.Stdout != "a b;printf bad >&2" {
		t.Fatalf("stdout = %q", got.Stdout)
	}
	if got.Stderr != "" {
		t.Fatalf("stderr = %q", got.Stderr)
	}
}

func TestHostRuntimeReadFileRangeReadsByteWindows(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "long.txt")
	want := "prefix-" + strings.Repeat("x", 10000) + "-suffix"
	if err := os.WriteFile(filename, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := NewHostRuntime(1024)
	first, err := rt.ReadFileRange(context.Background(), filename, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "prefix-" || first.TotalBytes != int64(len(want)) || first.NextByte != 7 || first.EndOfFile {
		t.Fatalf("unexpected first range: %#v", first)
	}
	second, err := rt.ReadFileRange(context.Background(), filename, first.NextByte, len(want))
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != want[7:] || !second.EndOfFile || second.NextByte != int64(len(want)) {
		t.Fatalf("unexpected second range: %#v", second)
	}
}

func TestHostRuntimeReadFileRangeClampsOffsets(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "short.txt")
	if err := os.WriteFile(filename, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := NewHostRuntime(1024).ReadFileRange(context.Background(), filename, 99, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.StartByte != 3 || got.NextByte != 3 || got.ReturnedBytes != 0 || !got.EndOfFile {
		t.Fatalf("unexpected clamped range: %#v", got)
	}
}
