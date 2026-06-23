package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/messaging/dedup"
)

const enqueueTimeout = 5 * time.Second

type Inbound struct {
	cfg    Config
	inbox  inbox.Inbox[events.AgentEvent]
	hc     *httpClient
	cursor string
	dedup  *dedup.Dedup
}

func NewInbound(cfg Config, agentInbox inbox.Inbox[events.AgentEvent]) *Inbound {
	return &Inbound{
		cfg:   cfg,
		inbox: agentInbox,
	}
}

func (i *Inbound) Run(ctx context.Context) error {
	i.dedup = dedup.NewDedup(ctx, dedupCapacity, dedupTTL)

	if err := i.ensureLogin(ctx); err != nil {
		return err
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := i.poll(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if isSessionExpired(err) {
				slog.Warn("wechat session expired, re-logging in")
				i.cursor = ""
				if loginErr := i.ensureLogin(ctx); loginErr != nil {
					return loginErr
				}
				continue
			}
			slog.Warn("wechat poll error, retrying in 5s", "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (i *Inbound) ensureLogin(ctx context.Context) error {
	if i.cfg.BotToken != "" {
		baseURL := i.cfg.BaseURL
		if baseURL == "" {
			baseURL = defaultBaseURL
		}
		i.hc = newHTTPClient(i.cfg.BotToken, baseURL)
		slog.Info("wechat using pre-configured bot token")
		return nil
	}
	return i.qrLogin(ctx)
}

type qrCodeResp struct {
	QRCode       string `json:"qrcode"`
	QRCodeImgURL string `json:"qrcode_img_content"`
}

type qrStatusResp struct {
	Status   string `json:"status"`
	BotToken string `json:"bot_token"`
	BotID    string `json:"ilink_bot_id"`
	UserID   string `json:"ilink_user_id"`
	BaseURL  string `json:"baseurl"`
}

func (i *Inbound) qrLogin(ctx context.Context) error {
	baseURL := i.cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	// Step 1: get QR code
	qrResp, err := getQRCode(ctx, baseURL)
	if err != nil {
		return fmt.Errorf("wechat get qrcode: %w", err)
	}
	slog.Info("qr code url obtained", "url", qrResp.QRCodeImgURL)

	// Step 2: poll for confirmed
	confirmed, err := i.pollQRStatus(ctx, baseURL, qrResp.QRCode)
	if err != nil {
		return fmt.Errorf("wechat qr status: %w", err)
	}

	effectiveBaseURL := confirmed.BaseURL
	if effectiveBaseURL == "" {
		effectiveBaseURL = baseURL
	}

	i.hc = newHTTPClient(confirmed.BotToken, effectiveBaseURL)
	slog.Info("wechat login confirmed", "bot_id", confirmed.BotID, "base_url", effectiveBaseURL, "bot_token", confirmed.BotToken)
	return nil
}

func getQRCode(ctx context.Context, baseURL string) (*qrCodeResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+pathGetQRCode+"?bot_type=3", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out qrCodeResp
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}

func (i *Inbound) pollQRStatus(ctx context.Context, baseURL, qrCode string) (*qrStatusResp, error) {
	client := &http.Client{Timeout: 40 * time.Second}
	url := baseURL + pathGetQRCodeStatus + "?qrcode=" + qrCode
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("iLink-App-ClientVersion", "1")

		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("wechat qr poll error, retrying", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		var status qrStatusResp
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			slog.Warn("wechat qr decode error, retrying", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()

		switch status.Status {
		case "confirmed":
			return &status, nil
		case "expired":
			slog.Info("wechat QR expired, requesting new one")
			newQR, err := getQRCode(ctx, baseURL)
			if err != nil {
				return nil, fmt.Errorf("re-request qrcode: %w", err)
			}
			fmt.Printf("\n[WeChat] QR code expired. New QR image URL: %s\n\n", newQR.QRCodeImgURL)
			url = baseURL + pathGetQRCodeStatus + "?qrcode=" + newQR.QRCode
		case "scaned":
			slog.Info("wechat QR scanned, waiting for confirmation")
			time.Sleep(1 * time.Second)
		default: // "wait" or unknown — keep polling
			time.Sleep(2 * time.Second)
		}
	}
}

type getUpdatesReq struct {
	GetUpdatesBuf string   `json:"get_updates_buf"`
	BaseInfo      baseInfo `json:"base_info"`
}

type getUpdatesResp struct {
	Ret           int     `json:"ret"`
	ErrCode       int     `json:"errcode"`
	Msgs          []wxMsg `json:"msgs"`
	GetUpdatesBuf string  `json:"get_updates_buf"`
}

type wxMsg struct {
	Seq          int64    `json:"seq"`
	MessageID    int64    `json:"message_id"`
	FromUserID   string   `json:"from_user_id"`
	ToUserID     string   `json:"to_user_id"`
	ClientID     string   `json:"client_id"`
	MessageType  int      `json:"message_type"`
	MessageState int      `json:"message_state"`
	ContextToken string   `json:"context_token"`
	ItemList     []wxItem `json:"item_list"`
}

type wxItem struct {
	Type     int         `json:"type"`
	TextItem *wxTextItem `json:"text_item,omitempty"`
}

type wxTextItem struct {
	Text string `json:"text"`
}

const (
	msgTypeUser  = 1
	itemTypeText = 1
)

func (i *Inbound) poll(ctx context.Context) error {
	req := getUpdatesReq{
		GetUpdatesBuf: i.cursor,
		BaseInfo:      newBaseInfo(),
	}
	var resp getUpdatesResp
	if err := i.hc.post(ctx, pathGetUpdates, req, &resp); err != nil {
		return err
	}
	if resp.Ret != 0 || resp.ErrCode == -14 {
		return sessionExpiredErr{}
	}

	i.cursor = resp.GetUpdatesBuf

	for _, msg := range resp.Msgs {
		if msg.MessageType != msgTypeUser {
			continue
		}
		if !i.dedup.Check(msg.ClientID) {
			continue
		}
		go i.processMessage(ctx, msg, i.hc)
	}
	return nil
}

func (i *Inbound) processMessage(ctx context.Context, msg wxMsg, hc *httpClient) {
	text := extractText(msg)
	if text == "" {
		return
	}
	outbound := NewOutbound(hc, msg.FromUserID, msg.ContextToken)
	i.enqueue(ctx, events.TextInputEvent{
		ChatID:    msg.FromUserID,
		MessageID: msg.ClientID,
		Message:   text,
		Sender:    outbound,
	})
}

func extractText(msg wxMsg) string {
	for _, item := range msg.ItemList {
		if item.Type == itemTypeText && item.TextItem != nil {
			return item.TextItem.Text
		}
	}
	return ""
}

func (i *Inbound) enqueue(ctx context.Context, ev events.AgentEvent) {
	ctx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()
	err := i.inbox.Publish(ctx, ev)
	if err != nil {
		slog.Warn("wechat enqueue failed", "err", err)
	}
}

type sessionExpiredErr struct{}

func (sessionExpiredErr) Error() string { return "wechat session expired (ret=-14)" }

func isSessionExpired(err error) bool {
	_, ok := err.(sessionExpiredErr)
	return ok
}
