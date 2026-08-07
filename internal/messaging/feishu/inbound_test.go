package feishu

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"my-bot/internal/runtime"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type recordingRuntime struct {
	runtime.Runtime
	content string
	path    string
	err     error
}

func (r *recordingRuntime) WriteTmpFile(ctx context.Context, content string) (string, error) {
	r.content = content
	return r.path, r.err
}

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

func TestParseFileContent(t *testing.T) {
	content, err := parseFileContent("{\"file_key\":\"file-key\",\"file_name\":\"../../report\\n.pdf\"}")
	if err != nil {
		t.Fatalf("parse file content: %v", err)
	}
	if content.FileKey != "file-key" || content.FileName != "../../report\n.pdf" {
		t.Fatalf("unexpected file content: %+v", content)
	}
}

func TestParseFileContentRequiresFileKey(t *testing.T) {
	for _, content := range []string{
		`{"file_name":"report.pdf"}`,
		`{"file_key":"   "}`,
		`not-json`,
	} {
		if _, err := parseFileContent(content); err == nil {
			t.Fatalf("expected error for %q", content)
		}
	}
}

func TestReadFile(t *testing.T) {
	data, err := readFile(strings.NewReader("123456"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "123456" {
		t.Fatalf("unexpected data: %q", data)
	}

	if _, err := readFile(nil); err == nil {
		t.Fatal("expected nil reader error")
	}
}

func TestFormatFileMessage(t *testing.T) {
	got := formatFileMessage("report\n.pdf", "./tmp/output-id", 42)
	want := "[RECEIVED FILE]\nfilename: \"report\\n.pdf\"\npath: ./tmp/output-id\nsize: 42 bytes"
	if got != want {
		t.Fatalf("unexpected file message:\n%s", got)
	}
}

func TestSaveFileDataUsesRuntimeTmpFile(t *testing.T) {
	rt := &recordingRuntime{path: "./tmp/output-id"}
	inbound := &Inbound{rt: rt}
	data := []byte{0, 1, 2, 255}

	path, err := inbound.saveFileData(context.Background(), data)
	if err != nil {
		t.Fatalf("save file data: %v", err)
	}
	if path != rt.path {
		t.Fatalf("path = %q, want %q", path, rt.path)
	}
	if !bytes.Equal([]byte(rt.content), data) {
		t.Fatalf("saved data = %v, want %v", []byte(rt.content), data)
	}
}
