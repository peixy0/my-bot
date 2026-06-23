package wechat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	rand "math/rand/v2"
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
	randUint := rand.Uint32()
	binary.BigEndian.PutUint32(b[:], randUint)
	return base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(b[:])))
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
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read error response (HTTP %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	if respBody != nil {
		return json.NewDecoder(resp.Body).Decode(respBody)
	}
	return nil
}
