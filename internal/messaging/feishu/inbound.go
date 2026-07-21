package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/messaging"
	"my-bot/internal/messaging/dedup"
	"my-bot/internal/runtime"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type Inbound struct {
	cfg    Config
	inbox  inbox.Inbox[events.AgentEvent]
	rt     runtime.Runtime
	client *lark.Client
	dedup  *dedup.Dedup
}

const (
	dedupCapacity  = 1024
	dedupTTL       = 5 * time.Minute
	enqueueTimeout = 5 * time.Second
	postImageLimit = 4
)

var (
	errMissingPostContentV2 = errors.New("missing post content_v2")
	markdownImagePattern    = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
)

type postData struct {
	text   string
	images []events.ImageData
}

func NewInbound(cfg Config, agentInbox inbox.Inbox[events.AgentEvent], rt runtime.Runtime) *Inbound {
	client := lark.NewClient(cfg.AppID, cfg.AppSecret,
		lark.WithLogLevel(larkcore.LogLevelWarn),
	)
	return &Inbound{
		cfg:    cfg,
		inbox:  agentInbox,
		rt:     rt,
		client: client,
	}
}

func (i *Inbound) Run(ctx context.Context) error {
	i.dedup = dedup.NewDedup(ctx, dedupCapacity, dedupTTL)

	d := dispatcher.NewEventDispatcher(i.cfg.VerificationToken, i.cfg.EncryptKey).
		OnP2MessageReceiveV1(i.onMessageReceive)

	cli := larkws.NewClient(i.cfg.AppID, i.cfg.AppSecret,
		larkws.WithEventHandler(d),
		larkws.WithLogLevel(larkcore.LogLevelError),
	)
	return cli.Start(ctx)
}

func (i *Inbound) onMessageReceive(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	msg := event.Event.Message
	msgID := *msg.MessageId
	if !i.dedup.Check(msgID) {
		return nil
	}

	chatID := *msg.ChatId
	outbound := NewOutbound(i.client, i.rt, chatID, msgID)
	switch *msg.MessageType {
	case "text":
		go i.processTextMessage(context.WithoutCancel(ctx), chatID, msgID, msg, outbound)
	case "image":
		go i.processImageMessage(context.WithoutCancel(ctx), chatID, msgID, msg, outbound)
	case "post":
		go i.processPostMessage(context.WithoutCancel(ctx), chatID, msgID, msg, outbound)
	default:
		slog.Warn("unsupported feishu message type", "type", *msg.MessageType, "chat_id", chatID, "message_id", msgID)
	}
	return nil
}

func (i *Inbound) sendError(ctx context.Context, outbound events.Outbound, msg, userMessage string, err error) {
	slog.Warn(msg, "err", err)
	outbound.Send(ctx, fmt.Sprintf("%s (%s)", userMessage, err.Error()))
}

func (i *Inbound) readImageData(ctx context.Context, msgID, msgBody string) (events.ImageData, error) {
	var body struct {
		ImageKey string `json:"image_key"`
	}
	if err := json.Unmarshal([]byte(msgBody), &body); err != nil {
		return events.ImageData{}, fmt.Errorf("unmarshal image msg body: %w", err)
	}
	return i.readImageDataByKey(ctx, msgID, body.ImageKey)
}

func (i *Inbound) readImageDataByKey(ctx context.Context, msgID, imageKey string) (events.ImageData, error) {
	var data []byte
	err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
		req := larkim.NewGetMessageResourceReqBuilder().
			MessageId(msgID).
			FileKey(imageKey).
			Type("image").
			Build()
		resp, err := i.client.Im.MessageResource.Get(ctx, req)
		if err != nil {
			return err
		}
		data, err = io.ReadAll(resp.File)
		return err
	})
	if err != nil {
		return events.ImageData{}, err
	}
	return events.ImageData{Data: data, MIMEType: http.DetectContentType(data)}, nil
}

func (i *Inbound) processTextMessage(
	ctx context.Context,
	chatID, msgID string,
	msg *larkim.EventMessage,
	outbound *Outbound) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(*msg.Content), &body); err != nil {
		i.sendError(ctx, outbound, "failed to unmarshal text message", "couldn't process your text", err)
		return
	}
	body.Text = replaceMentionKeys(body.Text, msg.Mentions)
	if msg.ParentId != nil {
		var parentMsgResp *larkim.GetMessageResp
		err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
			parentMsgReq := larkim.NewGetMessageReqBuilder().MessageId(*msg.ParentId).Build()
			resp, err := i.client.Im.Message.Get(ctx, parentMsgReq)
			if err != nil {
				return err
			}
			parentMsgResp = resp
			return nil
		})
		if err == nil {
			i.processBasedOnParentMessage(ctx, chatID, msgID, parentMsgResp.Data.Items, body.Text, outbound)
			return
		}
		slog.Warn("failed to read parent message", "chat_id", chatID, "message_id", msgID, "parent_id", *msg.ParentId, "err", err)
	}

	i.enqueue(ctx, events.TextInputEvent{
		ChatID:    chatID,
		MessageID: msgID,
		Message:   body.Text,
		Sender:    outbound,
	})
}

func (i *Inbound) processBasedOnParentMessage(
	ctx context.Context,
	chatID, msgID string,
	parentItems []*larkim.Message,
	content string,
	outbound *Outbound) {
	var sb strings.Builder
	var images []events.ImageData
	for index, item := range parentItems {
		var reference strings.Builder
		switch *item.MsgType {
		case "image":
			data, err := i.readImageData(ctx, *item.MessageId, *item.Body.Content)
			if err == nil {
				images = append(images, data)
				reference.WriteString(fmt.Sprintf("[IMAGE %d]\n", len(images)))
				break
			}
			slog.Warn("failed to read parent image", "chat_id", chatID, "message_id", *item.MessageId, "err", err)
			reference.WriteString("[IMAGE UNAVAILABLE]\n")
		case "text":
			text, err := readTextData(*item.Body.Content)
			if err != nil {
				slog.Warn("failed to read parent text", "chat_id", chatID, "message_id", *item.MessageId, "err", err)
				reference.WriteString("[TEXT UNAVAILABLE]\n")
				break
			}
			reference.WriteString(text)
			reference.WriteString("\n")
		case "post":
			post, err := i.readPostData(ctx, chatID, *item.MessageId, *item.Body.Content)
			if err != nil {
				slog.Warn("failed to read parent post", "chat_id", chatID, "message_id", *item.MessageId, "err", err)
				reference.WriteString("[POST UNAVAILABLE]\n")
				break
			}
			images = append(images, post.images...)
			reference.WriteString(post.text)
			reference.WriteString("\n")
		default:
			reference.WriteString(fmt.Sprintf("[UNSUPPORTED MESSAGE TYPE: %s]\n", *item.MsgType))
		}
		if len(parentItems) > 1 {
			sb.WriteString(fmt.Sprintf("BEGIN REFERENCE [%d]\n%sEND REFERENCE [%d]\n\n", index+1, reference.String(), index+1))
			continue
		}
		sb.WriteString(reference.String())
	}
	message := ""
	if len(parentItems) > 1 {
		message = fmt.Sprintf("%s%s", sb.String(), content)
	} else {
		message = fmt.Sprintf("BEGIN REFERENCE\n%s\nEND REFERENCE\n\n%s", sb.String(), content)
	}
	if len(images) > 0 {
		i.enqueue(ctx, events.ImageInputEvent{
			ChatID:    chatID,
			MessageID: msgID,
			ImageData: images,
			Message:   message,
			Sender:    outbound,
		})
		return
	}
	i.enqueue(ctx, events.TextInputEvent{
		ChatID:    chatID,
		MessageID: msgID,
		Message:   message,
		Sender:    outbound,
	})
}

func (i *Inbound) processImageMessage(
	ctx context.Context,
	chatID, msgID string,
	msg *larkim.EventMessage,
	outbound *Outbound,
) {
	data, err := i.readImageData(ctx, msgID, *msg.Content)
	if err != nil {
		i.sendError(ctx, outbound, "failed to read image data", "couldn't process your image", err)
		return
	}
	i.enqueue(ctx, events.ImageInputEvent{
		ChatID:    chatID,
		MessageID: msgID,
		ImageData: []events.ImageData{data},
		Sender:    outbound,
	})
}

func (i *Inbound) processPostMessage(
	ctx context.Context,
	chatID, msgID string,
	msg *larkim.EventMessage,
	outbound *Outbound,
) {
	post, err := i.readPostData(ctx, chatID, msgID, *msg.Content)
	if err != nil {
		if errors.Is(err, errMissingPostContentV2) {
			slog.Warn("missing post content_v2", "chat_id", chatID, "message_id", msgID)
			return
		}
		i.sendError(ctx, outbound, "failed to read post message", "couldn't process your message", err)
		return
	}
	if len(post.images) > 0 {
		i.enqueue(ctx, events.ImageInputEvent{
			ChatID:    chatID,
			MessageID: msgID,
			ImageData: post.images,
			Message:   post.text,
			Sender:    outbound,
		})
		return
	}
	if strings.TrimSpace(post.text) == "" {
		slog.Warn("post has no supported content", "chat_id", chatID, "message_id", msgID)
		return
	}
	i.enqueue(ctx, events.TextInputEvent{
		ChatID:    chatID,
		MessageID: msgID,
		Message:   post.text,
		Sender:    outbound,
	})
}

func readTextData(content string) (string, error) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		return "", fmt.Errorf("unmarshal text msg body: %w", err)
	}
	return body.Text, nil
}

func replaceMentionKeys(text string, mentions []*larkim.MentionEvent) string {
	for _, mention := range mentions {
		if mention.Key == nil || mention.Name == nil {
			continue
		}
		text = strings.ReplaceAll(text, *mention.Key, "@"+*mention.Name)
	}
	return text
}

func (i *Inbound) readPostData(ctx context.Context, chatID, msgID, content string) (postData, error) {
	var body struct {
		Title     string              `json:"title"`
		ContentV2 [][]json.RawMessage `json:"content_v2"`
	}
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		return postData{}, fmt.Errorf("unmarshal post msg body: %w", err)
	}
	if len(body.ContentV2) == 0 {
		return postData{}, errMissingPostContentV2
	}

	post := postData{}
	var text strings.Builder
	if strings.TrimSpace(body.Title) != "" {
		text.WriteString(body.Title)
		text.WriteString("\n\n")
	}
	imageCount := 0
	appendImage := func(key string) string {
		imageCount++
		number := imageCount
		if number > postImageLimit {
			return fmt.Sprintf("[IMAGE %d OMITTED]", number)
		}
		marker := fmt.Sprintf("[IMAGE %d]", number)
		data, err := i.readImageDataByKey(ctx, msgID, key)
		if err != nil {
			slog.Warn("failed to read post image", "chat_id", chatID, "message_id", msgID, "err", err)
			return fmt.Sprintf("[IMAGE %d UNAVAILABLE]", number)
		}
		post.images = append(post.images, data)
		return marker
	}

	for _, row := range body.ContentV2 {
		var line strings.Builder
		for _, raw := range row {
			var element struct {
				Tag      string `json:"tag"`
				Text     string `json:"text"`
				Href     string `json:"href"`
				UserName string `json:"user_name"`
				ImageKey string `json:"image_key"`
			}
			if err := json.Unmarshal(raw, &element); err != nil {
				return postData{}, fmt.Errorf("unmarshal post element: %w", err)
			}
			switch element.Tag {
			case "text":
				line.WriteString(element.Text)
			case "a":
				line.WriteString(fmt.Sprintf("[%s](%s)", element.Text, element.Href))
			case "at":
				if element.UserName != "" {
					line.WriteString("@")
					line.WriteString(element.UserName)
				}
			case "img":
				if element.ImageKey != "" {
					line.WriteString(appendImage(element.ImageKey))
				}
			case "md":
				line.WriteString(markdownImagePattern.ReplaceAllStringFunc(element.Text, func(markdownImage string) string {
					matches := markdownImagePattern.FindStringSubmatch(markdownImage)
					return appendImage(matches[1])
				}))
			default:
				slog.Warn("unsupported post element", "tag", element.Tag, "chat_id", chatID, "message_id", msgID)
			}
		}
		lineText := strings.TrimSpace(line.String())
		if lineText == "" {
			continue
		}
		if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n\n") {
			text.WriteString("\n")
		}
		text.WriteString(lineText)
		text.WriteString("\n")
	}
	post.text = strings.TrimSpace(text.String())
	return post, nil
}

func (i *Inbound) enqueue(ctx context.Context, ev events.AgentEvent) bool {
	ctx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()
	err := i.inbox.Publish(ctx, ev)
	if err == nil {
		return true
	}
	if ctx.Err() == context.DeadlineExceeded {
		slog.Warn("feishu enqueue timeout", "event", fmt.Sprintf("%T", ev), "timeout", enqueueTimeout)
	} else {
		slog.Warn("feishu enqueue canceled", "event", fmt.Sprintf("%T", ev), "err", err)
	}
	return false
}
