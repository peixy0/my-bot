package feishu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"my-bot/internal/runtime"
	"my-bot/internal/util"

	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	streamingElementID = "agent_markdown"
	streamingMinPush   = 250 * time.Millisecond
)

type Outbound struct {
	client    *lark.Client
	rt        runtime.Runtime
	chatID    string
	messageID string
	mu        sync.Mutex
	partial   strings.Builder
	stream    *streamingCard
}

type streamingCard struct {
	cardID   string
	sequence int
	lastPush time.Time
	failed   bool
}

func NewOutbound(client *lark.Client, rt runtime.Runtime, chatID, messageID string) *Outbound {
	return &Outbound{
		client:    client,
		rt:        rt,
		chatID:    chatID,
		messageID: messageID,
	}
}

func (o *Outbound) Send(ctx context.Context, text string) {
	const maxRetry = 3
	var err error
	for attempt := 1; attempt <= maxRetry; attempt++ {
		err = o.SendText(ctx, text)
		if err == nil {
			return
		}
	}
	slog.Error("feishu send failed after retries", "err", err, "chat_id", o.chatID, "attempts", maxRetry)
}

func (o *Outbound) SendDelta(ctx context.Context, text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.partial.WriteString(text)

	if strings.TrimSpace(o.partial.String()) == "" {
		return
	}
	if o.stream != nil && o.stream.failed {
		return
	}
	if o.stream == nil {
		stream, err := o.openStreamingCard(ctx, o.partial.String())
		if err != nil {
			slog.Warn("feishu streaming card open failed; falling back to batch send", "err", err, "chat_id", o.chatID)
			o.stream = &streamingCard{failed: true}
			return
		}
		o.stream = stream
		return
	}
	if time.Since(o.stream.lastPush) < streamingMinPush {
		return
	}
	if err := o.updateStreamingCard(ctx, o.stream, o.partial.String()); err != nil {
		slog.Warn("feishu streaming card update failed", "err", err, "chat_id", o.chatID, "card_id", o.stream.cardID)
	}
}

func (o *Outbound) SendFinal(ctx context.Context) {
	o.mu.Lock()
	text := o.partial.String()
	o.partial.Reset()
	stream := o.stream
	o.stream = nil
	o.mu.Unlock()

	if strings.TrimSpace(text) == "" {
		if stream != nil && !stream.failed {
			if err := o.closeStreamingCard(ctx, stream); err != nil {
				slog.Warn("feishu streaming card close failed", "err", err, "chat_id", o.chatID, "card_id", stream.cardID)
			}
		}
		return
	}
	if stream != nil && !stream.failed {
		if err := o.updateStreamingCard(ctx, stream, text); err != nil {
			slog.Warn("feishu streaming card final update failed", "err", err, "chat_id", o.chatID, "card_id", stream.cardID)
		}
		if err := o.closeStreamingCard(ctx, stream); err != nil {
			slog.Warn("feishu streaming card close failed", "err", err, "chat_id", o.chatID, "card_id", stream.cardID)
		}
		return
	}
	o.Send(ctx, text)
}

func (o *Outbound) SendText(ctx context.Context, text string) error {
	card := map[string]any{
		"schema": "2.0",
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":     "markdown",
					"content": text,
				},
			},
		},
	}
	content, _ := util.ToJSON(card)

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(o.chatID).
			MsgType("interactive").
			Content(string(content)).
			Build()).
		Build()
	_, err := o.client.Im.Message.Create(ctx, req)
	if err != nil {
		slog.Error("feishu send error", "err", err, "chat_id", o.chatID)
		return err
	}

	slog.Debug("feishu message sent", "chat_id", o.chatID)
	return nil
}

func (o *Outbound) openStreamingCard(ctx context.Context, text string) (*streamingCard, error) {
	cardJSON, err := util.ToJSON(streamingCardPayload(text, true))
	if err != nil {
		return nil, fmt.Errorf("marshal streaming card: %w", err)
	}
	createReq := larkcardkit.NewCreateCardReqBuilder().
		Body(larkcardkit.NewCreateCardReqBodyBuilder().
			Type("card_json").
			Data(string(cardJSON)).
			Build()).
		Build()
	createResp, err := o.client.Cardkit.V1.Card.Create(ctx, createReq)
	if err != nil {
		return nil, fmt.Errorf("create streaming card: %w", err)
	}
	if !createResp.Success() || createResp.Data == nil || createResp.Data.CardId == nil || *createResp.Data.CardId == "" {
		return nil, fmt.Errorf("create streaming card: %s", createResp.CodeError.String())
	}

	cardID := *createResp.Data.CardId
	content, _ := util.ToJSON(map[string]any{
		"type": "card",
		"data": map[string]any{"card_id": cardID},
	})
	sendReq := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(o.chatID).
			MsgType("interactive").
			Content(string(content)).
			Build()).
		Build()
	if _, err := o.client.Im.Message.Create(ctx, sendReq); err != nil {
		return nil, fmt.Errorf("send streaming card: %w", err)
	}
	return &streamingCard{cardID: cardID, lastPush: time.Now()}, nil
}

func (o *Outbound) updateStreamingCard(ctx context.Context, stream *streamingCard, text string) error {
	stream.sequence++
	req := larkcardkit.NewContentCardElementReqBuilder().
		CardId(stream.cardID).
		ElementId(streamingElementID).
		Body(larkcardkit.NewContentCardElementReqBodyBuilder().
			Uuid(uuid.NewString()).
			Content(text).
			Sequence(stream.sequence).
			Build()).
		Build()
	resp, err := o.client.Cardkit.V1.CardElement.Content(ctx, req)
	if err != nil {
		return fmt.Errorf("update streaming card: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("update streaming card: %s", resp.CodeError.String())
	}
	stream.lastPush = time.Now()
	return nil
}

func (o *Outbound) closeStreamingCard(ctx context.Context, stream *streamingCard) error {
	settings, err := util.ToJSON(map[string]any{
		"config": map[string]any{"streaming_mode": false},
	})
	if err != nil {
		return fmt.Errorf("marshal streaming card settings: %w", err)
	}
	stream.sequence++
	req := larkcardkit.NewSettingsCardReqBuilder().
		CardId(stream.cardID).
		Body(larkcardkit.NewSettingsCardReqBodyBuilder().
			Uuid(uuid.NewString()).
			Settings(string(settings)).
			Sequence(stream.sequence).
			Build()).
		Build()
	resp, err := o.client.Cardkit.V1.Card.Settings(ctx, req)
	if err != nil {
		return fmt.Errorf("close streaming card: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("close streaming card: %s", resp.CodeError.String())
	}
	return nil
}

func streamingCardPayload(content string, streaming bool) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"streaming_mode": streaming,
			"summary":        map[string]any{"content": ""},
			"streaming_config": map[string]any{
				"print_frequency_ms": map[string]any{"default": 70},
				"print_step":         map[string]any{"default": 1},
				"print_strategy":     "fast",
			},
		},
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":        "markdown",
					"content":    content,
					"element_id": streamingElementID,
				},
			},
		},
	}
}

func (o *Outbound) StartThinking(_ context.Context) {}
func (o *Outbound) EndThinking(_ context.Context)   {}

func (o *Outbound) sendImage(ctx context.Context, data []byte) (string, error) {
	uploadReq := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType("message").
			Image(bytes.NewReader(data)).
			Build()).
		Build()
	uploadResp, err := o.client.Im.Image.Create(ctx, uploadReq)
	if err != nil {
		return "", err
	}
	if uploadResp.Data == nil || uploadResp.Data.ImageKey == nil {
		return "", errors.New("failed to upload image: no image key returned")
	}
	imageKey := uploadResp.Data.ImageKey
	content, _ := util.ToJSON(map[string]any{"image_key": *imageKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(o.chatID).
			MsgType("image").
			Content(string(content)).
			Build()).
		Build()
	_, err = o.client.Im.Message.Create(ctx, req)
	return *imageKey, err
}

func (o *Outbound) sendFile(ctx context.Context, name string, data []byte) error {
	uploadReq := larkim.NewCreateFileReqBuilder().
		Body(larkim.NewCreateFileReqBodyBuilder().
			FileType("stream").
			FileName(name).
			File(bytes.NewReader(data)).
			Build()).
		Build()
	uploadResp, err := o.client.Im.File.Create(ctx, uploadReq)
	if err != nil {
		return err
	}
	fileKey := uploadResp.Data.FileKey
	content, _ := util.ToJSON(map[string]any{"file_key": *fileKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(o.chatID).
			MsgType("file").
			Content(string(content)).
			Build()).
		Build()
	_, err = o.client.Im.Message.Create(ctx, req)
	return err
}

func (o *Outbound) addReaction(ctx context.Context, emoji string) error {
	req := larkim.NewCreateMessageReactionReqBuilder().
		MessageId(o.messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(emoji).Build()).
			Build()).
		Build()
	_, err := o.client.Im.MessageReaction.Create(ctx, req)
	return err
}
