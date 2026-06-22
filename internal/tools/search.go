package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type SearchResult struct {
	Title string `json:"title"`
	Href  string `json:"href"`
	Body  string `json:"body"`
}

type WebSearch struct {
	baseAPI string
	client  *http.Client
}

func NewWebSearch(baseAPI string) *WebSearch {
	return &WebSearch{
		baseAPI: baseAPI,
		client: &http.Client{
			Transport: &http.Transport{},
			Timeout:   3 * time.Minute,
		},
	}
}

func (w *WebSearch) Search(ctx context.Context, query string, page int) ([]SearchResult, error) {
	reqURL := fmt.Sprintf("%s?query=%s&page=%d", w.baseAPI, url.QueryEscape(query), page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBodySize))
	if err != nil {
		return nil, err
	}

	var searchResponse struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.Unmarshal(body, &searchResponse); err != nil {
		return []SearchResult{}, fmt.Errorf("unmarshal search results: %w", err)
	}
	return searchResponse.Results, nil
}
