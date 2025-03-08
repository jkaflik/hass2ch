package ch

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Client is an HTTP client for ClickHouse
type Client struct {
	url url.URL
}

func NewClient(serverURL string) (*Client, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ClickHouse URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported ClickHouse URL scheme: %s", u.Scheme)
	}

	queryParams := u.Query()
	queryParams.Set("async_insert", "1")
	queryParams.Set("input_format_skip_unknown_fields", "1")
	u.RawQuery = queryParams.Encode()

	return &Client{url: *u}, nil
}

func (c *Client) Insert(query string, data io.Reader) error {
	uri := c.url
	queryParams := uri.Query()
	queryParams.Set("query", query)
	uri.RawQuery = queryParams.Encode()

	resp, err := http.Post(uri.String(), "text/plain", data)
	if err != nil {
		return fmt.Errorf("failed to send insert request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("insert failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
