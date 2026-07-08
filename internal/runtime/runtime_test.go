package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
