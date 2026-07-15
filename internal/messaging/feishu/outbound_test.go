package feishu

import "testing"

func TestStreamingCardPayload(t *testing.T) {
	payload := streamingCardPayload("hello", true)

	if payload["schema"] != "2.0" {
		t.Fatalf("expected schema 2.0, got %#v", payload["schema"])
	}
	config, ok := payload["config"].(map[string]any)
	if !ok {
		t.Fatalf("expected config map, got %#v", payload["config"])
	}
	if config["streaming_mode"] != true {
		t.Fatalf("expected streaming mode enabled, got %#v", config["streaming_mode"])
	}
	if config["update_multi"] != true {
		t.Fatalf("expected update_multi enabled, got %#v", config["update_multi"])
	}
	body, ok := payload["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected body map, got %#v", payload["body"])
	}
	elements, ok := body["elements"].([]map[string]any)
	if !ok || len(elements) != 1 {
		t.Fatalf("expected one markdown element, got %#v", body["elements"])
	}
	element := elements[0]
	if element["tag"] != "markdown" || element["content"] != "hello" || element["element_id"] != streamingElementID {
		t.Fatalf("unexpected streaming element: %#v", element)
	}
}
