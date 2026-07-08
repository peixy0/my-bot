package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func buildCDNUploadURL(uploadParam, fileKey string) string {
	return cdnBaseURL + "/upload?encrypted_query_param=" + url.QueryEscape(uploadParam) + "&filekey=" + url.QueryEscape(fileKey)
}

func generateAESKey() ([]byte, error) {
	key := make([]byte, 16)
	_, err := rand.Read(key)
	return key, err
}

// parseAESKey handles the three key encoding formats used by WeChat CDN:
//   - 32 hex chars → hex decode → 16 bytes
//   - base64(hex string) → base64 decode → hex decode → 16 bytes
//   - base64(raw 16 bytes) → base64 decode → 16 bytes
func parseAESKey(s string) ([]byte, error) {
	if len(s) == 32 {
		return hex.DecodeString(s)
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("parseAESKey: not valid base64: %w", err)
		}
	}
	if len(decoded) == 16 {
		return decoded, nil
	}
	// assume decoded is a hex string
	return hex.DecodeString(string(decoded))
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	padding := bytes.Repeat([]byte{byte(pad)}, pad)
	return append(data, padding...)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("pkcs7Unpad: empty data")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize {
		return nil, fmt.Errorf("pkcs7Unpad: invalid padding %d", pad)
	}
	return data[:len(data)-pad], nil
}

func encryptAES128ECB(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(data, aes.BlockSize)
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}
	return out, nil
}

func decryptAES128ECB(key, data []byte) ([]byte, error) {
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("decryptAES128ECB: data length %d is not a multiple of block size", len(data))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return pkcs7Unpad(out)
}

var cdnHTTPClient = &http.Client{Timeout: 60 * time.Second}

func cdnDownload(ctx context.Context, fullURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cdnDownload: build request: %w", err)
	}
	resp, err := cdnHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cdnDownload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("cdnDownload: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// cdnUpload uploads AES-encrypted bytes to the CDN and returns the
// x-encrypted-param header value from the response.
func cdnUpload(ctx context.Context, uploadURL string, encData []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(encData))
	if err != nil {
		return "", fmt.Errorf("cdnUpload: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := cdnHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cdnUpload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("cdnUpload: HTTP %d: %s", resp.StatusCode, body)
	}
	encParam := resp.Header.Get("x-encrypted-param")
	if encParam == "" {
		return "", fmt.Errorf("cdnUpload: missing x-encrypted-param header")
	}
	return encParam, nil
}
