package fishaudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the default Fish Audio API base URL.
	DefaultBaseURL = "https://api.fish.audio"

	// DefaultTimeout is the default request timeout.
	DefaultTimeout = 240 * time.Second

	// Version is the SDK version.
	Version = "1.1.1" // x-release-please-version
)

// userAgent is the SDK's product, fish-audio-go/<Version>: the format every
// Fish Audio SDK sends (one RFC 9110 product, token "/" token), so the API can
// tell which SDK and version made a request.
const userAgent = "fish-audio-go/" + Version

// userAgentWith returns the User-Agent to send: the SDK's product, followed by
// the caller's value if there is one. A caller's value is appended rather than
// allowed to replace the SDK's, as in the other Fish Audio SDKs, so an app can
// name itself without hiding which SDK made the request.
func userAgentWith(caller string) string {
	caller = strings.TrimSpace(caller)
	if caller == userAgent || strings.HasPrefix(caller, userAgent+" ") {
		caller = strings.TrimSpace(caller[len(userAgent):])
	}
	if caller == "" {
		return userAgent
	}
	return userAgent + " " + caller
}

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
// If no API key is provided via WithAPIKey, it will fall back to the FISH_API_KEY environment variable.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		baseURL: DefaultBaseURL,
		timeout: DefaultTimeout,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	// Fall back to env var if no API key was set via options
	if c.apiKey == "" {
		c.apiKey = os.Getenv("FISH_API_KEY")
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
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Apply request options
	if opts != nil {
		for k, v := range opts.AdditionalHeaders {
			if http.CanonicalHeaderKey(k) == "User-Agent" {
				req.Header.Set("User-Agent", userAgentWith(v))
				continue
			}
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
