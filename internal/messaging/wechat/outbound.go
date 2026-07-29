package wechat

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"strings"
	"time"

	"my-bot/internal/messaging"
	"my-bot/internal/runtime"
)

const maxChunkLen = 2000

type Outbound struct {
	hc           *httpClient
	fromUserID   string
	contextToken string
	partial      strings.Builder
	rt           runtime.Runtime
}

func NewOutbound(hc *httpClient, fromUserID, contextToken string, rt runtime.Runtime) *Outbound {
	return &Outbound{
		hc:           hc,
		fromUserID:   fromUserID,
		contextToken: contextToken,
		rt:           rt,
	}
}

func (o *Outbound) Send(ctx context.Context, text string) {
	for _, chunk := range splitText(text, maxChunkLen) {
		if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
			return o.sendText(ctx, chunk)
		}); err != nil {
			slog.Warn("wechat send failed", "err", err, "user", o.fromUserID)
		}
	}
}

func (o *Outbound) SendBegin(_ context.Context) {}

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
	return messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
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
	})
}

func newClientID() string {
	return fmt.Sprintf("mybot:%d-%08x", time.Now().UnixMilli(), mrand.Uint32())
}

func newFileKey() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

type getUploadURLReq struct {
	FileKey     string   `json:"filekey"`
	MediaType   int      `json:"media_type"`
	ToUserID    string   `json:"to_user_id"`
	RawSize     int64    `json:"rawsize"`
	RawFileMD5  string   `json:"rawfilemd5"`
	FileSize    int64    `json:"filesize"`
	AESKey      string   `json:"aeskey"`
	NoNeedThumb bool     `json:"no_need_thumb"`
	BaseInfo    baseInfo `json:"base_info"`
}

type getUploadURLResp struct {
	UploadParam      string `json:"upload_param"`
	ThumbUploadParam string `json:"thumb_upload_param,omitempty"`
	UploadFullURL    string `json:"upload_full_url"`
}

const (
	mediaTypeImage = 1
	mediaTypeFile  = 3
)

func rawMD5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func (o *Outbound) uploadMedia(ctx context.Context, mediaType int, fileKey string, data []byte) (CDNMedia, error) {
	aesKey, err := generateAESKey()
	if err != nil {
		return CDNMedia{}, fmt.Errorf("generate AES key: %w", err)
	}
	encData, err := encryptAES128ECB(aesKey, data)
	if err != nil {
		return CDNMedia{}, fmt.Errorf("encrypt: %w", err)
	}

	uploadReq := getUploadURLReq{
		FileKey:     fileKey,
		MediaType:   mediaType,
		ToUserID:    o.fromUserID,
		RawSize:     int64(len(data)),
		RawFileMD5:  rawMD5(data),
		FileSize:    int64(len(encData)),
		AESKey:      hex.EncodeToString(aesKey),
		NoNeedThumb: true,
		BaseInfo:    newBaseInfo(),
	}
	var uploadResp getUploadURLResp
	err = messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
		return o.hc.post(ctx, pathGetUploadURL, uploadReq, &uploadResp)
	})
	if err != nil {
		return CDNMedia{}, fmt.Errorf("getuploadurl: %w", err)
	}

	uploadURL := strings.TrimSpace(uploadResp.UploadFullURL)
	if uploadURL == "" {
		if uploadResp.UploadParam == "" {
			return CDNMedia{}, fmt.Errorf("getuploadurl returned neither upload_full_url nor upload_param")
		}
		uploadURL = buildCDNUploadURL(uploadResp.UploadParam, fileKey)
	}

	encQueryParam, err := cdnUpload(ctx, uploadURL, encData)
	if err != nil {
		return CDNMedia{}, fmt.Errorf("cdn upload: %w", err)
	}

	return CDNMedia{
		EncryptQueryParam: encQueryParam,
		AESKey:            base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(aesKey))),
		EncryptType:       1,
	}, nil
}

func (o *Outbound) sendImage(ctx context.Context, data []byte) error {
	media, err := o.uploadMedia(ctx, mediaTypeImage, newFileKey(), data)
	if err != nil {
		return err
	}
	return messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
		req := sendMessageReq{
			Msg: wxSendMsg{
				ToUserID:     o.fromUserID,
				ClientID:     newClientID(),
				MessageType:  msgTypeBot,
				MessageState: msgStateFinish,
				ContextToken: o.contextToken,
				ItemList: []wxItem{
					{Type: itemTypeImage, ImageItem: &wxImageItem{
						Media: media,
					}},
				},
			},
			BaseInfo: newBaseInfo(),
		}
		return o.hc.post(ctx, pathSendMessage, req, nil)
	})
}

func (o *Outbound) sendFile(ctx context.Context, filename string, data []byte) error {
	media, err := o.uploadMedia(ctx, mediaTypeFile, newFileKey(), data)
	if err != nil {
		return err
	}
	return messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
		req := sendMessageReq{
			Msg: wxSendMsg{
				ToUserID:     o.fromUserID,
				ClientID:     newClientID(),
				MessageType:  msgTypeBot,
				MessageState: msgStateFinish,
				ContextToken: o.contextToken,
				ItemList: []wxItem{
					{Type: itemTypeFile, FileItem: &wxFileItem{
						Media:    media,
						FileName: filename,
						Len:      fmt.Sprintf("%d", len(data)),
					}},
				},
			},
			BaseInfo: newBaseInfo(),
		}
		return o.hc.post(ctx, pathSendMessage, req, nil)
	})
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
