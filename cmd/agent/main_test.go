package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBuildWebSocketURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  webUIConfig
		want string
	}{
		{
			name: "host port",
			cfg:  webUIConfig{host: "127.0.0.1", port: 8017},
			want: "ws://127.0.0.1:8017/api/bot",
		},
		{
			name: "query token",
			cfg:  webUIConfig{host: "127.0.0.1", port: 8017, token: "secret"},
			want: "ws://127.0.0.1:8017/api/bot?token=secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildWebSocketURL(tt.cfg)
			if err != nil {
				t.Fatalf("buildWebSocketURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("buildWebSocketURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutputStateWaitsForThinkingEnd(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	out := &outputState{stdout: &stdout, stderr: &stderr, lastByteNewline: true}

	done, err := out.handle(serverFrame{Type: "message_stream_delta", Text: "hello"})
	if err != nil || done {
		t.Fatalf("first delta done=%v err=%v", done, err)
	}
	done, err = out.handle(serverFrame{Type: "message_stream_end"})
	if err != nil || done {
		t.Fatalf("stream end done=%v err=%v", done, err)
	}
	done, err = out.handle(serverFrame{Type: "message_stream_delta", Text: "world"})
	if err != nil || done {
		t.Fatalf("second delta done=%v err=%v", done, err)
	}
	done, err = out.handle(serverFrame{Type: "thinking_end"})
	if err != nil {
		t.Fatalf("thinking_end error = %v", err)
	}
	if !done {
		t.Fatal("expected thinking_end to complete the one-shot session")
	}
	if got, want := stdout.String(), "hello\nworld\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestLoadWebUIConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("webui:\n  host: 0.0.0.0\n  port: 9001\n  token: secret\nllm:\n  api_key: ignored\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := loadWebUIConfig(path)
	if err != nil {
		t.Fatalf("loadWebUIConfig() error = %v", err)
	}
	if got.host != "0.0.0.0" || got.port != 9001 || got.token != "secret" {
		t.Fatalf("config = %+v", got)
	}
}

func TestLoadWebUIConfigRequiresFile(t *testing.T) {
	_, err := loadWebUIConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected missing config to fail")
	}
}

func TestRunOneShotWebSocket(t *testing.T) {
	received := make(chan promptFrame, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bot" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("token") != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var raw json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			t.Errorf("read prompt: %v", err)
			return
		}
		var prompt promptFrame
		if err := json.Unmarshal(raw, &prompt); err != nil {
			t.Errorf("decode prompt: %v", err)
			return
		}
		received <- prompt

		frames := []serverFrame{
			{Type: "connected", ChatID: "session-test"},
			{Type: "thinking_start", ChatID: "session-test"},
			{Type: "message_stream_delta", Text: "hello"},
			{Type: "message_stream_end"},
			{Type: "message_stream_delta", Text: " world"},
			{Type: "thinking_end"},
		}
		for _, frame := range frames {
			if err := conn.WriteJSON(frame); err != nil {
				t.Errorf("write frame: %v", err)
				return
			}
		}
	}))
	defer srv.Close()

	parsedURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, ok := strings.Cut(parsedURL.Host, ":")
	if !ok {
		t.Fatalf("test server host has no port: %s", parsedURL.Host)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configData := []byte("webui:\n  host: " + host + "\n  port: " + port + "\n  token: secret\n")
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := run(ctx, options{prompt: "do work", configPath: configPath}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	select {
	case prompt := <-received:
		if prompt.Type != "text" || prompt.Message != "do work" || !strings.HasPrefix(prompt.MessageID, "cli-") {
			t.Fatalf("prompt frame = %+v", prompt)
		}
	default:
		t.Fatal("server did not receive prompt frame")
	}
	if got, want := stdout.String(), "hello\n world\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
