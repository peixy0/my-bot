package engine

import (
	"context"
	"testing"

	"my-bot/internal/config"
	"my-bot/internal/events"
)

func TestWorkerConfigChangeModel(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Model:       "gpt-4o",
			Temperature: 1,
			TopP:        1,
		},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyModel, Value: "gpt-4o-mini", Sender: out}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worker.Model() != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %s", worker.Model())
	}
	if len(out.messages) != 1 || out.messages[0] != "model set to: gpt-4o-mini" {
		t.Fatalf("unexpected response: %v", out.messages)
	}
}

func TestWorkerConfigChangeModelWithPreset(t *testing.T) {
	temp := 0.6
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Model:       "gpt-4o",
			Temperature: 1,
			TopP:        1,
		},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
		Models: map[string]config.ModelConfig{
			"Qwen3-32B": {Temperature: &temp},
		},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyModel, Value: "Qwen3-32B", Sender: out}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worker.Model() != "Qwen3-32B" {
		t.Fatalf("expected model Qwen3-32B, got %s", worker.Model())
	}
	if worker.cfg.LLM.Temperature != 0.6 {
		t.Fatalf("expected preset temperature 0.6, got %f", worker.cfg.LLM.Temperature)
	}
	if !containsMsg(out.messages, "model preset applied") {
		t.Fatalf("expected preset applied message, got %v", out.messages)
	}
}

func TestWorkerConfigChangeTemperature(t *testing.T) {
	cfg := &config.Config{
		LLM:  config.LLMConfig{Temperature: 1, TopP: 1},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyTemperature, Value: "0.5", Sender: out}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worker.Temperature() != "0.5" {
		t.Fatalf("expected temperature 0.5, got %s", worker.Temperature())
	}
}

func TestWorkerConfigChangeTemperatureOutOfRange(t *testing.T) {
	cfg := &config.Config{
		LLM:  config.LLMConfig{Temperature: 1, TopP: 1},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	for _, val := range []string{"3.0", "-0.1"} {
		ev := events.ConfigChangeEvent{Key: events.ConfigKeyTemperature, Value: val, Sender: out}
		if err := worker.processConfigChange(context.Background(), ev); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if !containsMsg(out.messages, "usage: /temperature <0..2>") {
		t.Fatalf("expected rejection message, got %v", out.messages)
	}
	if worker.Temperature() != "1" {
		t.Fatalf("temperature should not change on invalid input, got %s", worker.Temperature())
	}
}

func TestWorkerConfigChangeTopP(t *testing.T) {
	cfg := &config.Config{
		LLM:  config.LLMConfig{Temperature: 1, TopP: 1},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyTopP, Value: "0.8", Sender: out}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worker.TopP() != "0.8" {
		t.Fatalf("expected top_p 0.8, got %s", worker.TopP())
	}
}

func TestWorkerConfigChangeTopPOutOfRange(t *testing.T) {
	cfg := &config.Config{
		LLM:  config.LLMConfig{Temperature: 1, TopP: 1},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyTopP, Value: "1.5", Sender: out}
	worker.processConfigChange(context.Background(), ev)

	if !containsMsg(out.messages, "usage: /top_p <0..1>") {
		t.Fatalf("expected rejection, got %v", out.messages)
	}
	if worker.TopP() != "1" {
		t.Fatalf("top_p should not change, got %s", worker.TopP())
	}
}

func TestWorkerConfigChangeTopK(t *testing.T) {
	cfg := &config.Config{
		LLM:  config.LLMConfig{Temperature: 1, TopP: 1},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyTopK, Value: "50", Sender: out}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worker.TopK() != "50" {
		t.Fatalf("expected top_k 50, got %s", worker.TopK())
	}
}

func TestWorkerConfigChangeTopKRejectsZeroAndNegative(t *testing.T) {
	cfg := &config.Config{
		LLM:  config.LLMConfig{Temperature: 1, TopP: 1},
		Tool: config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	for _, val := range []string{"0", "-1"} {
		ev := events.ConfigChangeEvent{Key: events.ConfigKeyTopK, Value: val, Sender: out}
		worker.processConfigChange(context.Background(), ev)
	}
	if !containsMsg(out.messages, "usage: /top_k <positive integer>") {
		t.Fatalf("expected rejection, got %v", out.messages)
	}
}

func TestWorkerConfigChangeMaxTokens(t *testing.T) {
	cfg := &config.Config{
		LLM:     config.LLMConfig{Temperature: 1, TopP: 1},
		Context: config.ContextConfig{MaxOutputTokens: 16384, WindowTokens: 128000, CompressionThreshold: 0.7},
		Tool:    config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyMaxTokens, Value: "8192", Sender: out}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worker.MaxTokens() != "8192" {
		t.Fatalf("expected max_tokens 8192, got %s", worker.MaxTokens())
	}
}

func TestWorkerConfigChangeMaxTokensRejectsZero(t *testing.T) {
	cfg := &config.Config{
		LLM:     config.LLMConfig{Temperature: 1, TopP: 1},
		Context: config.ContextConfig{MaxOutputTokens: 16384, WindowTokens: 128000, CompressionThreshold: 0.7},
		Tool:    config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyMaxTokens, Value: "0", Sender: out}
	worker.processConfigChange(context.Background(), ev)

	if !containsMsg(out.messages, "usage: /max_tokens <positive integer>") {
		t.Fatalf("expected rejection, got %v", out.messages)
	}
}

func TestWorkerConfigChangeContextWindow(t *testing.T) {
	cfg := &config.Config{
		LLM:     config.LLMConfig{Temperature: 1, TopP: 1},
		Context: config.ContextConfig{MaxOutputTokens: 16384, WindowTokens: 128000, CompressionThreshold: 0.7},
		Tool:    config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyContextWindow, Value: "64000", Sender: out}
	if err := worker.processConfigChange(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if worker.ContextWindow() != "64000" {
		t.Fatalf("expected context_window 64000, got %s", worker.ContextWindow())
	}
}

func TestWorkerConfigChangeContextWindowRejectsNegative(t *testing.T) {
	cfg := &config.Config{
		LLM:     config.LLMConfig{Temperature: 1, TopP: 1},
		Context: config.ContextConfig{MaxOutputTokens: 16384, WindowTokens: 128000, CompressionThreshold: 0.7},
		Tool:    config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)
	out := &captureOutbound{}

	ev := events.ConfigChangeEvent{Key: events.ConfigKeyContextWindow, Value: "-1", Sender: out}
	worker.processConfigChange(context.Background(), ev)

	if !containsMsg(out.messages, "usage: /context_window <positive integer>") {
		t.Fatalf("expected rejection, got %v", out.messages)
	}
}

func TestWorkerConfigQueryAll(t *testing.T) {
	cfg := &config.Config{
		LLM:     config.LLMConfig{Model: "gpt-4o", Temperature: 1, TopP: 1},
		Context: config.ContextConfig{MaxOutputTokens: 16384, WindowTokens: 128000, CompressionThreshold: 0.7},
		Vision:  config.VisionConfig{Enabled: true},
		Tool:    config.ToolConfig{MaxOutputChars: 1000},
	}
	worker := newConfigTestWorker(cfg)

	queries := map[string]string{
		events.ConfigKeyModel:         "current model: gpt-4o",
		events.ConfigKeyVision:        "current vision: on",
		events.ConfigKeyTemperature:   "current temperature: 1",
		events.ConfigKeyTopP:          "current top_p: 1",
		events.ConfigKeyMaxTokens:     "current max_tokens: 16384",
		events.ConfigKeyContextWindow: "current context_window: 128000",
	}

	for key, want := range queries {
		out := &captureOutbound{}
		ev := events.ConfigQueryEvent{Key: key, Sender: out}
		if err := worker.processConfigQuery(context.Background(), ev); err != nil {
			t.Fatalf("query %s: %v", key, err)
		}
		if len(out.messages) != 1 || out.messages[0] != want {
			t.Fatalf("query %s: expected %q, got %v", key, want, out.messages)
		}
	}
}

func containsMsg(msgs []string, substr string) bool {
	for _, m := range msgs {
		if contains(m, substr) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
