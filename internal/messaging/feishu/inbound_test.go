package feishu

import (
	"context"
	"errors"
	"strings"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestReplaceMentionKeys(t *testing.T) {
	key := "@_user_1"
	name := "Ada"
	text := replaceMentionKeys("@_user_1 please review this with @_user_2", []*larkim.MentionEvent{{
		Key:  &key,
		Name: &name,
	}})
	if text != "@Ada please review this with @_user_2" {
		t.Fatalf("unexpected mention replacement: %q", text)
	}
}

func TestReadPostDataContentV2(t *testing.T) {
	inbound := &Inbound{}
	post, err := inbound.readPostData(context.Background(), "chat", "msg", `{
		"title":"Release",
		"content_v2":[
			[
				{"tag":"text","text":"Before"}
			],
			[
				{"tag":"a","text":"Docs","href":"https://example.com"},
				{"tag":"at","user_name":"Ada","user_id":"@_user_1"}
			]
		]
	}`)
	if err != nil {
		t.Fatalf("read post data: %v", err)
	}
	for _, want := range []string{
		"Release",
		"Before",
		"[Docs](https://example.com)@Ada",
	} {
		if !strings.Contains(post.text, want) {
			t.Fatalf("expected %q in %q", want, post.text)
		}
	}
	if len(post.images) != 0 {
		t.Fatalf("expected no images, got %d", len(post.images))
	}
}

func TestMarkdownImagePattern(t *testing.T) {
	match := markdownImagePattern.FindStringSubmatch("![image](img_one)")
	if len(match) != 2 || match[1] != "img_one" {
		t.Fatalf("unexpected markdown image match: %q", match)
	}
}

func TestReadPostDataRequiresContentV2(t *testing.T) {
	_, err := (&Inbound{}).readPostData(context.Background(), "chat", "msg", `{"content":[[{"tag":"text","text":"legacy"}]]}`)
	if !errors.Is(err, errMissingPostContentV2) {
		t.Fatalf("expected missing content_v2 error, got %v", err)
	}
}
