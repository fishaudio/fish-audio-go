package fishaudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	// DefaultBaseURL is the default Fish Audio API base URL.
	DefaultBaseURL = "https://api.fish.audio"

	// DefaultTimeout is the default request timeout.
	DefaultTimeout = 240 * time.Second

	// Version is the SDK version.
	Version = "0.1.0"
)

// Client is the Fish Audio API client.
type Client struct {
	apiKey     string
	baseURL    string
	timeout    time.Duration
	httpClient *http.Client

	// Services
	TTS     *TTSService
	ASR     *ASRService
	Voices  *VoicesService
	Account *AccountService
}

// NewClient creates a new Fish Audio API client.
//
// If apiKey is empty, it will try to read from the FISH_API_KEY environment variable.
func NewClient(apiKey string, opts ...ClientOption) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("FISH_API_KEY")
	}

	c := &Client{
		apiKey:  apiKey,
		baseURL: DefaultBaseURL,
		timeout: DefaultTimeout,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	// Update HTTP client timeout if changed
	c.httpClient.Timeout = c.timeout

	// Initialize services
	c.TTS = &TTSService{client: c}
	c.ASR = &ASRService{client: c}
	c.Voices = &VoicesService{client: c}
	c.Account = &AccountService{client: c}

	return c
}

// Close closes the HTTP client's idle connections.
func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// doRequest performs an HTTP request with authentication.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, opts *RequestOptions) (*http.Response, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "fish-audio/go/"+Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Apply request options
	if opts != nil {
		for k, v := range opts.AdditionalHeaders {
			req.Header.Set(k, v)
		}
		if len(opts.AdditionalQueryParams) > 0 {
			q := req.URL.Query()
			for k, v := range opts.AdditionalQueryParams {
				q.Add(k, v)
			}
			req.URL.RawQuery = q.Encode()
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, newAPIError(resp.StatusCode, resp.Status, string(bodyBytes))
	}

	return resp, nil
}

// doJSONRequest performs an HTTP request and decodes the JSON response.
func (c *Client) doJSONRequest(ctx context.Context, method, path string, body interface{}, result interface{}, opts *RequestOptions) error {
	resp, err := c.doRequest(ctx, method, path, body, opts)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}
