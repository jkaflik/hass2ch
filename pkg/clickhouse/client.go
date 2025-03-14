package clickhouse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/jkaflik/hass2ch/internal/metrics"
	"github.com/jkaflik/hass2ch/pkg/retry"
)

// RetryConfig defines the retry configuration for ClickHouse operations
type RetryConfig struct {
	MaxRetries          int
	InitialInterval     time.Duration
	MaxInterval         time.Duration
	Multiplier          float64
	RandomizationFactor float64
}

// DefaultRetryConfig returns the default retry configuration for ClickHouse operations
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:          5,
		InitialInterval:     500 * time.Millisecond,
		MaxInterval:         30 * time.Second,
		Multiplier:          2.0,
		RandomizationFactor: 0.5,
	}
}

// Client is an HTTP client for ClickHouse
type Client struct {
	url        url.URL
	username   string
	password   string
	httpClient *http.Client
	retryConf  RetryConfig
}

// ClientOption is a function that configures a Client
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client for the ClickHouse client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithRetryConfig sets a custom retry configuration for the ClickHouse client
func WithRetryConfig(retryConf RetryConfig) ClientOption {
	return func(c *Client) {
		c.retryConf = retryConf
	}
}

func NewClient(serverURL, username, password string, options ...ClientOption) (*Client, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ClickHouse URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported ClickHouse URL scheme: %s", u.Scheme)
	}

	queryParams := u.Query()
	queryParams.Set("async_insert", "1")
	queryParams.Set("date_time_input_format", "best_effort")
	queryParams.Set("enable_json_type", "1")
	queryParams.Set("input_format_skip_unknown_fields", "1")
	queryParams.Set("input_format_json_read_bools_as_strings", "1")
	queryParams.Set("input_format_json_read_numbers_as_strings", "1")
	queryParams.Set("input_format_json_read_arrays_as_strings", "1")
	u.RawQuery = queryParams.Encode()

	client := &Client{
		url:        *u,
		username:   username,
		password:   password,
		httpClient: http.DefaultClient,
		retryConf:  DefaultRetryConfig(),
	}

	// Apply options
	for _, option := range options {
		option(client)
	}

	return client, nil
}

// isRetryableError determines if an error from ClickHouse should be retried
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for network errors
	if retry.IsNetworkError(err) {
		return true
	}

	// Check for specific ClickHouse error messages that indicate transient issues
	errMsg := err.Error()

	// Check for server availability issues
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "no route to host") ||
		strings.Contains(errMsg, "i/o timeout") {
		return true
	}

	// Check for ClickHouse specific errors that are recoverable
	if strings.Contains(errMsg, "Too many parts") ||
		strings.Contains(errMsg, "Memory limit") ||
		strings.Contains(errMsg, "DB::Exception: Timeout") ||
		strings.Contains(errMsg, "No space left on device") {
		return true
	}

	// HTTP 5xx errors
	if strings.Contains(errMsg, "status 500") ||
		strings.Contains(errMsg, "status 502") ||
		strings.Contains(errMsg, "status 503") ||
		strings.Contains(errMsg, "status 504") {
		return true
	}

	return false
}

// Execute runs a query on ClickHouse with retries for transient failures
func (c *Client) Execute(ctx context.Context, query string, r io.Reader) error {
	// If the reader is of a reusable type (like bytes.Buffer), we can retry with it
	// For non-reusable readers, we need to buffer it first if we want to retry
	var buf []byte
	var err error

	if r != nil {
		// Check if reader is a bytes.Buffer which we can reuse
		if _, ok := r.(*bytes.Buffer); !ok {
			// Not a bytes.Buffer, need to read it fully once
			buf, err = io.ReadAll(r)
			if err != nil {
				return fmt.Errorf("failed to read request body: %w", err)
			}
			// Replace reader with a new one for each retry
			r = nil // This ensures we use the buffer instead
		}
	}

	// Convert retry config to generic retry config
	retryConfig := retry.Config{
		MaxRetries:          c.retryConf.MaxRetries,
		InitialInterval:     c.retryConf.InitialInterval,
		MaxInterval:         c.retryConf.MaxInterval,
		Multiplier:          c.retryConf.Multiplier,
		RandomizationFactor: c.retryConf.RandomizationFactor,
	}

	// Define retry callbacks for metrics
	callbacks := retry.Callbacks{
		OnRetryAttempt: func(attempt int, err error, nextBackoff time.Duration) {
			metrics.CHRetryAttempts.Inc()
			log.Warn().
				Err(err).
				Int("attempt", attempt).
				Dur("next_backoff", nextBackoff).
				Msg("Retrying ClickHouse operation")
		},
		OnRetrySuccess: func(attempt int) {
			metrics.CHRetrySuccess.Inc()
			log.Info().
				Int("attempt", attempt).
				Msg("ClickHouse operation succeeded after retry")
		},
		OnRetryFailure: func(attempt int, err error) {
			log.Error().
				Err(err).
				Int("attempt", attempt).
				Msg("ClickHouse operation failed after all retries")
		},
	}

	return retry.DoWithCallbacks(ctx, func() error {
		var bodyReader = r

		// If we have a buffer, create a new reader for each retry
		if buf != nil {
			bodyReader = bytes.NewReader(buf)
		}

		// Build the URL with the query parameter
		uri := c.url
		queryParams := uri.Query()
		queryParams.Set("query", query)
		uri.RawQuery = queryParams.Encode()

		// Create a new request for each retry
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), bodyReader)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("User-Agent", "hass2ch")
		req.SetBasicAuth(c.username, c.password)

		// Execute the query
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
		defer resp.Body.Close()

		// Check for HTTP errors
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("query execution failed with status %d: %s", resp.StatusCode, string(body))
		}

		return nil
	}, isRetryableError, retryConfig, callbacks)
}
