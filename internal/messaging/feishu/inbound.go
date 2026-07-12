package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
)

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
	}
	return nil
}

func (i *Inbound) sendError(ctx context.Context, outbound events.Outbound, msg string, err error) {
	slog.Warn(msg, "err", err)
	outbound.Send(ctx, fmt.Sprintf("couldn't process your image (%s)", err.Error()))
}

func (i *Inbound) readImageData(ctx context.Context, msgID, msgBody string) ([]byte, error) {
	var body struct {
		ImageKey string `json:"image_key"`
	}
	if err := json.Unmarshal([]byte(msgBody), &body); err != nil {
		return nil, fmt.Errorf("unmarshal image msg body: %w", err)
	}
	var data []byte
	err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
		req := larkim.NewGetMessageResourceReqBuilder().
			MessageId(msgID).
			FileKey(body.ImageKey).
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
		return nil, err
	}
	return data, nil
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
		i.sendError(ctx, outbound, "failed to unmarshal text message", err)
		return
	}
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
	for _, item := range parentItems {
		if *item.MsgType == "image" {
			data, err := i.readImageData(ctx, *item.MessageId, *item.Body.Content)
			if err == nil {
				i.enqueue(ctx, events.ImageInputEvent{
					ChatID:    chatID,
					MessageID: msgID,
					ImageData: data,
					MIMEType:  "image/jpeg",
					Sender:    outbound,
					Message:   content,
				})
				return
			}
			sb.WriteString("[IMAGE]\n")
		} else if *item.MsgType == "text" {
			sb.WriteString(*item.Body.Content)
			sb.WriteString("\n")
		} else {
			sb.WriteString(fmt.Sprintf("[UNSUPPORTED MESSAGE TYPE: %s]\n", *item.MsgType))
		}
	}
	i.enqueue(ctx, events.TextInputEvent{
		ChatID:    chatID,
		MessageID: msgID,
		Message:   fmt.Sprintf("BEGIN REFERENCE\n%s\nEND REFERENCE\n\n%s", sb.String(), content),
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
		i.sendError(ctx, outbound, "failed to read image data", err)
		return
	}
	i.enqueue(ctx, events.ImageInputEvent{
		ChatID:    chatID,
		MessageID: msgID,
		ImageData: data,
		MIMEType:  "image/jpeg",
		Sender:    outbound,
	})
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
