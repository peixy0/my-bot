package feishu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"my-bot/internal/events"
	"my-bot/internal/messaging"
	"my-bot/internal/runtime"
	"my-bot/internal/util"

	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	streamingElementID       = "agent_markdown"
	streamingFooterElementID = "agent_footer"
	streamingMinPush         = 1000 * time.Millisecond
)

type Outbound struct {
	client    *lark.Client
	rt        runtime.Runtime
	chatID    string
	messageID string
	partial   strings.Builder
	stream    *streamingCard
	lastPush  time.Time
}

type streamingCard struct {
	cardID   string
	sequence int
}

func NewOutbound(client *lark.Client, rt runtime.Runtime, chatID, messageID string) *Outbound {
	return &Outbound{
		client:    client,
		rt:        rt,
		chatID:    chatID,
		messageID: messageID,
		lastPush:  time.Time{},
	}
}

func (o *Outbound) Send(ctx context.Context, text string) {
	if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
		return o.SendText(ctx, text)
	}); err != nil {
		slog.Warn("feishu send failed", "err", err, "chat_id", o.chatID)
	}
}

func (o *Outbound) SendBegin(ctx context.Context) {
	o.partial.Reset()
	if o.stream != nil {
		if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
			return o.updateStreamingCardElement(ctx, o.stream, streamingElementID, " ")
		}); err != nil {
			slog.Warn("feishu streaming card clear failed", "err", err, "chat_id", o.chatID, "card_id", o.stream.cardID)
			o.stream = nil
		}
		return
	}
}

func (o *Outbound) SendDelta(ctx context.Context, text string) {
	o.partial.WriteString(text)

	if strings.TrimSpace(o.partial.String()) == "" {
		return
	}
	if time.Since(o.lastPush) < streamingMinPush {
		return
	}
	o.lastPush = time.Now()
	if o.stream == nil {
		var stream *streamingCard
		if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
			var err error
			stream, err = o.openStreamingCard(ctx, o.partial.String())
			return err
		}); err != nil {
			slog.Warn("feishu streaming card open failed; falling back to batch send", "err", err, "chat_id", o.chatID)
			return
		}
		o.stream = stream
		return
	}
	if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
		return o.updateStreamingCardElement(ctx, o.stream, streamingElementID, o.partial.String())
	}); err != nil {
		slog.Warn("feishu streaming card update failed", "err", err, "chat_id", o.chatID, "card_id", o.stream.cardID)
	}
}

func (o *Outbound) SendFinal(ctx context.Context, metadata *events.ResponseMetadata) {
	text := o.partial.String()
	o.partial.Reset()
	stream := o.stream
	o.stream = nil
	hasText := strings.TrimSpace(text) != ""
	footer := formatResponseMetadata(metadata)

	updateFailed := false
	if stream != nil {
		if hasText {
			if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
				return o.updateStreamingCardElement(ctx, stream, streamingElementID, text)
			}); err != nil {
				slog.Warn("feishu streaming card final update failed", "err", err, "chat_id", o.chatID, "card_id", stream.cardID)
				updateFailed = true
			}
		}
		if footer != "" {
			if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
				return o.updateStreamingCardElement(ctx, stream, streamingFooterElementID, footer)
			}); err != nil {
				slog.Warn("feishu streaming card footer update failed", "err", err, "chat_id", o.chatID, "card_id", stream.cardID)
				updateFailed = true
			}
		}
		if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
			return o.closeStreamingCard(ctx, stream)
		}); err != nil {
			slog.Warn("feishu streaming card close failed", "err", err, "chat_id", o.chatID, "card_id", stream.cardID)
		}
	}
	if hasText && (stream == nil || updateFailed) {
		if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
			return o.sendCard(ctx, text, footer)
		}); err != nil {
			slog.Warn("feishu send failed", "err", err, "chat_id", o.chatID)
		}
	}
}

func (o *Outbound) SendText(ctx context.Context, text string) error {
	return o.sendCard(ctx, text, "")
}

func (o *Outbound) sendCard(ctx context.Context, text, footer string) error {
	card := cardPayload(text, footer, false)
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
	return err
}

func (o *Outbound) openStreamingCard(ctx context.Context, text string) (*streamingCard, error) {
	cardJSON, err := util.ToJSON(cardPayload(text, "", true))
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
	return &streamingCard{cardID: cardID}, nil
}

func (o *Outbound) updateStreamingCardElement(ctx context.Context, stream *streamingCard, elementID, text string) error {
	stream.sequence++
	req := larkcardkit.NewContentCardElementReqBuilder().
		CardId(stream.cardID).
		ElementId(elementID).
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
	return nil
}

func (o *Outbound) closeStreamingCard(ctx context.Context, stream *streamingCard) error {
	stream.sequence++
	settings, err := util.ToJSON(map[string]any{
		"config": map[string]any{"streaming_mode": false},
	})
	if err != nil {
		return fmt.Errorf("marshal streaming card settings: %w", err)
	}
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

func cardPayload(content, footer string, streaming bool) map[string]any {
	elements := []map[string]any{
		{
			"tag":        "markdown",
			"content":    content,
			"element_id": streamingElementID,
		},
	}
	if footer != "" || streaming {
		elements = append(elements, map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":        "plain_text",
				"content":    footer,
				"text_size":  "notation",
				"text_color": "grey",
				"element_id": streamingFooterElementID,
			},
		})
	}
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"streaming_mode": streaming,
			"update_multi":   true,
			"summary":        map[string]any{"content": ""},
			"streaming_config": map[string]any{
				"print_frequency_ms": map[string]any{"default": 70},
				"print_step":         map[string]any{"default": 1},
				"print_strategy":     "fast",
			},
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
}

func formatResponseMetadata(metadata *events.ResponseMetadata) string {
	if metadata == nil {
		return ""
	}
	var parts []string
	if model := strings.TrimSpace(metadata.Model); model != "" {
		parts = append(parts, model)
	}
	if metadata.CompletionTokens > 0 && metadata.GenerationTime > 0 {
		parts = append(parts, fmt.Sprintf("%.1f tokens/s", float64(metadata.CompletionTokens)/metadata.GenerationTime.Seconds()))
	}
	if metadata.TotalTokens > 0 {
		total := formatTokenCount(metadata.TotalTokens)
		if metadata.ContextWindow > 0 {
			parts = append(parts, fmt.Sprintf("%s / %s tokens (%.1f%%)", total, formatTokenCount(metadata.ContextWindow), float64(metadata.TotalTokens)*100/float64(metadata.ContextWindow)))
		} else {
			parts = append(parts, total+" tokens")
		}
	}
	return strings.Join(parts, " · ")
}

func formatTokenCount(value int64) string {
	text := strconv.FormatInt(value, 10)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
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
		return "", fmt.Errorf("upload image: %w", err)
	}
	if uploadResp.Data == nil || uploadResp.Data.ImageKey == nil {
		return "", errors.New("failed to upload image: no image key returned")
	}

	imageKey := *uploadResp.Data.ImageKey
	content, _ := util.ToJSON(map[string]any{"image_key": imageKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(o.chatID).
			MsgType("image").
			Content(string(content)).
			Build()).
		Build()
	if _, err := o.client.Im.Message.Create(ctx, req); err != nil {
		return "", fmt.Errorf("send image message: %w", err)
	}
	return imageKey, nil
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
		return fmt.Errorf("upload file: %w", err)
	}

	fileKey := *uploadResp.Data.FileKey
	content, _ := util.ToJSON(map[string]any{"file_key": fileKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(o.chatID).
			MsgType("file").
			Content(string(content)).
			Build()).
		Build()
	if _, err := o.client.Im.Message.Create(ctx, req); err != nil {
		return fmt.Errorf("send file message: %w", err)
	}
	return nil
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
