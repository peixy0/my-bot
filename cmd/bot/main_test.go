package main

import (
	"reflect"
	"testing"
)

func TestRestartArgs(t *testing.T) {
	got := restartArgs("/usr/local/bin/bot", "/etc/my-bot/config.yaml")
	want := []string{"/usr/local/bin/bot", "-c", "/etc/my-bot/config.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restart args = %v, want %v", got, want)
	}
}
