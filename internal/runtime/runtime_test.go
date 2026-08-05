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
	got, err := NewHostRuntime(1024).Glob(context.Background(), pattern)
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
