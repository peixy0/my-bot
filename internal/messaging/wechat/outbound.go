package wechat

import (
	"context"
	"fmt"
	"log/slog"
	rand "math/rand/v2"
	"strings"
	"time"
)

const maxChunkLen = 2000

type Outbound struct {
	hc           *httpClient
	fromUserID   string
	contextToken string
	partial      strings.Builder
}

func NewOutbound(hc *httpClient, fromUserID, contextToken string) *Outbound {
	return &Outbound{
		hc:           hc,
		fromUserID:   fromUserID,
		contextToken: contextToken,
	}
}

func (o *Outbound) Send(ctx context.Context, text string) {
	for _, chunk := range splitText(text, maxChunkLen) {
		if err := o.sendText(ctx, chunk); err != nil {
			slog.Error("wechat send failed", "err", err, "user", o.fromUserID)
		}
	}
}

func (o *Outbound) SendDelta(_ context.Context, text string) {
	o.partial.WriteString(text)
}

func (o *Outbound) SendFinal(ctx context.Context) {
	text := o.partial.String()
	o.partial.Reset()
	if strings.TrimSpace(text) == "" {
		return
	}
	o.Send(ctx, text)
}

func (o *Outbound) StartThinking(_ context.Context) {}
func (o *Outbound) EndThinking(_ context.Context)   {}

type sendMessageReq struct {
	Msg      wxSendMsg `json:"msg"`
	BaseInfo baseInfo  `json:"base_info"`
}

type wxSendMsg struct {
	FromUserID   string   `json:"from_user_id"`
	ToUserID     string   `json:"to_user_id"`
	ClientID     string   `json:"client_id"`
	MessageType  int      `json:"message_type"`
	MessageState int      `json:"message_state"`
	ContextToken string   `json:"context_token"`
	ItemList     []wxItem `json:"item_list"`
}

const (
	msgTypeBot     = 2
	msgStateFinish = 2
)

func (o *Outbound) sendText(ctx context.Context, text string) error {
	req := sendMessageReq{
		Msg: wxSendMsg{
			FromUserID:   "",
			ToUserID:     o.fromUserID,
			ClientID:     newClientID(),
			MessageType:  msgTypeBot,
			MessageState: msgStateFinish,
			ContextToken: o.contextToken,
			ItemList: []wxItem{
				{
					Type:     itemTypeText,
					TextItem: &wxTextItem{Text: text},
				},
			},
		},
		BaseInfo: newBaseInfo(),
	}
	return o.hc.post(ctx, pathSendMessage, req, nil)
}

func newClientID() string {
	return fmt.Sprintf("mybot:%d-%08x", time.Now().UnixMilli(), rand.Uint32())
}

func splitText(text string, maxLen int) []string {
	if len([]rune(text)) <= maxLen {
		return []string{text}
	}
	var chunks []string
	runes := []rune(text)
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}
		cut := maxLen
		if i := lastIndexRune(runes[:cut], '\n'); i > maxLen/2 {
			cut = i + 1
		}
		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}
	return chunks
}

func lastIndexRune(s []rune, r rune) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == r {
			return i
		}
	}
	return -1
}
