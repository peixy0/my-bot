package tools

import (
	"context"
	"encoding/json"
	"errors"
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

func WebSearch(ctx context.Context, baseAPI, query string, page int) ([]SearchResult, error) {
	transport := &http.Transport{}
	client := &http.Client{
		Transport: transport,
		Timeout:   3 * time.Minute,
	}

	reqURL := fmt.Sprintf("%s?query=%s&page=%d", baseAPI, url.QueryEscape(query), page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var searchResponse struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.Unmarshal(body, &searchResponse); err != nil {
		return []SearchResult{}, errors.New(string(body))
	}
	return searchResponse.Results, nil
}
