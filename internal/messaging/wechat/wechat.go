package wechat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

type Config struct {
	BotToken string `yaml:"bot_token"`
	BaseURL  string `yaml:"base_url"`
}

const (
	defaultBaseURL = "https://ilinkai.weixin.qq.com"
	channelVersion = "2.0.0"
	dedupCapacity  = 1024
	dedupTTL       = 5 * time.Minute

	pathGetQRCode       = "/ilink/bot/get_bot_qrcode"
	pathGetQRCodeStatus = "/ilink/bot/get_qrcode_status"
	pathGetUpdates      = "/ilink/bot/getupdates"
	pathSendMessage     = "/ilink/bot/sendmessage"
)

type baseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

func newBaseInfo() baseInfo {
	return baseInfo{ChannelVersion: channelVersion}
}

func randomWechatUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	n := binary.BigEndian.Uint32(b[:])
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", n)))
}

type httpClient struct {
	inner    *http.Client
	botToken string
	baseURL  string
}

func newHTTPClient(botToken, baseURL string) *httpClient {
	return &httpClient{
		inner:    &http.Client{Timeout: 45 * time.Second},
		botToken: botToken,
		baseURL:  baseURL,
	}
}

func (h *httpClient) post(ctx context.Context, path string, reqBody, respBody any) error {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Authorization", "Bearer "+h.botToken)
	req.Header.Set("X-WECHAT-UIN", randomWechatUIN())

	resp, err := h.inner.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	if respBody != nil {
		return json.NewDecoder(resp.Body).Decode(respBody)
	}
	return nil
}

type dedup struct {
	in chan dedupReq
}

type dedupReq struct {
	id   string
	resp chan bool
}

func newDedup(ctx context.Context, capacity int, ttl time.Duration) *dedup {
	d := &dedup{in: make(chan dedupReq)}
	go d.run(ctx, capacity, ttl)
	return d
}

func (d *dedup) check(id string) bool {
	resp := make(chan bool, 1)
	select {
	case d.in <- dedupReq{id: id, resp: resp}:
		return <-resp
	case <-time.After(time.Second):
		return true
	}
}

func (d *dedup) run(ctx context.Context, capacity int, ttl time.Duration) {
	expires := make(map[string]time.Time, capacity)
	order := make([]string, 0, capacity)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-d.in:
			now := time.Now()
			for len(order) > 0 {
				if exp, ok := expires[order[0]]; ok && now.After(exp) {
					delete(expires, order[0])
					order = order[1:]
					continue
				}
				break
			}
			if _, dup := expires[req.id]; dup {
				req.resp <- false
				continue
			}
			if len(order) >= capacity {
				delete(expires, order[0])
				order = order[1:]
			}
			expires[req.id] = now.Add(ttl)
			order = append(order, req.id)
			req.resp <- true
		}
	}
}
