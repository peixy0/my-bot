package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"time"

	"my-bot/internal/runtime"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

type Fetcher struct {
	client         *http.Client
	fs             runtime.FileSystem
	truncater      runtime.Truncater
	maxOutputChars int
}

func NewFetcher(fs runtime.FileSystem, truncater runtime.Truncater, proxyURL string, maxOutputChars int) *Fetcher {
	transport := &http.Transport{}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			slog.Warn("invalid fetch proxy, ignoring", "proxy", proxyURL, "err", err)
		} else {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &Fetcher{
		client: &http.Client{
			Transport: transport,
			Timeout:   3 * time.Minute,
		},
		fs:             fs,
		truncater:      truncater,
		maxOutputChars: maxOutputChars,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (ToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return ErrorResult(fmt.Errorf("create fetch request for %s: %w", rawURL, err)), nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bot/1.0)")

	resp, err := f.client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Errorf("fetch %s: %w", rawURL, err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrorResult(fmt.Errorf("request failed with status code: %d", resp.StatusCode)), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBodySize))
	if err != nil {
		return ErrorResult(fmt.Errorf("read response body from %s: %w", rawURL, err)), nil
	}
	contentType := resp.Header.Get("content-type")

	mediaType, _, _ := mime.ParseMediaType(contentType)

	switch mediaType {
	case "text/html":
		markdown, err := htmltomarkdown.ConvertReader(bytes.NewReader(body))
		if err != nil {
			return ErrorResult(fmt.Errorf("failed to convert HTML to Markdown: %w", err)), nil
		}
		return TextResult(f.truncater.Truncate(ctx, string(markdown), f.maxOutputChars)), nil

	case "text/markdown", "text/x-markdown", "text/plain", "application/json":
		return TextResult(f.truncater.Truncate(ctx, string(body), f.maxOutputChars)), nil

	default:
		path, err := f.fs.WriteTmpFile(ctx, string(body))
		if err != nil {
			return TextResult(fmt.Sprintf("Content-Type: %s\n\n[output not readable]", contentType)), nil
		}
		return TextResult(fmt.Sprintf("Content-Type: %s\n\n[output saved to %s]", contentType, path)), nil
	}
}
