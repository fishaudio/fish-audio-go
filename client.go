package fishaudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
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

// product is the SDK's product, fish-audio-go/<Version>: the format every Fish
// Audio SDK sends (one RFC 9110 product, token "/" token), so the API can tell
// which SDK and version made a request.
const product = "fish-audio-go/" + Version

// userAgent is what the SDK sends: the product and a comment with the Go
// version and the OS, e.g. "fish-audio-go/1.2.0 (go 1.24.1; linux)". It is
// computed once.
var userAgent = buildUserAgent(runtime.Version(), runtime.GOOS)

// buildUserAgent returns "<product> (go <go version>; <os>)". It includes
// only the runtime, its version and the OS family: nothing that identifies a
// person or machine.
func buildUserAgent(goVersion, goos string) string {
	version := strings.TrimPrefix(commentValue(goVersion), "go")
	if version == "" {
		version = "unknown"
	}
	return product + " (go " + version + "; " + commentValue(strings.ToLower(goos)) + ")"
}

// commentValue makes a value safe inside an RFC 9110 comment. It keeps the
// leading run of letters, digits and "._+-" (at most 32 bytes) and drops the
// rest, so "(", ")", "\", whitespace and control characters never reach the
// header. It returns "unknown" when nothing usable is left.
func commentValue(value string) string {
	value = strings.TrimSpace(value)
	end := 0
	for end < len(value) && end < 32 && isCommentSafe(value[end]) {
		end++
	}
	if end == 0 {
		return "unknown"
	}
	return value[:end]
}

func isCommentSafe(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '+' || c == '-'
}

// userAgentWith returns the User-Agent to send: the SDK's product and runtime
// comment, followed by the caller's value if there is one. A caller's value is
// appended rather than allowed to replace the SDK's, as in the other Fish
// Audio SDKs, so an app can name itself without hiding which SDK made the
// request.
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
		return nil, newAPIError(resp.StatusCode, resp.Status, string(bodyBytes), resp.Header)
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
