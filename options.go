package fishaudio

import (
	"net/http"
	"time"
)

// ClientOption is a function that configures the Client.
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL for the API.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithTimeout sets the default timeout for requests.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.timeout = timeout
	}
}

// RequestOptions allows per-request overrides of client defaults.
type RequestOptions struct {
	// Timeout overrides the client's default timeout.
	Timeout time.Duration

	// AdditionalHeaders are extra headers to include in the request.
	AdditionalHeaders map[string]string

	// AdditionalQueryParams are extra query parameters to include.
	AdditionalQueryParams map[string]string
}

// WebSocketOptions configures WebSocket connections.
type WebSocketOptions struct {
	// PingTimeout is the maximum delay to wait for a pong response.
	// Default: 20 seconds.
	PingTimeout time.Duration

	// PingInterval is the interval for sending ping messages.
	// Default: 20 seconds.
	PingInterval time.Duration

	// MaxMessageSize is the maximum message size in bytes.
	// Default: 65536 bytes (64 KiB).
	MaxMessageSize int64

	// ReadBufferSize is the size of the read buffer.
	ReadBufferSize int

	// WriteBufferSize is the size of the write buffer.
	WriteBufferSize int
}

// DefaultWebSocketOptions returns WebSocketOptions with default values.
func DefaultWebSocketOptions() *WebSocketOptions {
	return &WebSocketOptions{
		PingTimeout:     20 * time.Second,
		PingInterval:    20 * time.Second,
		MaxMessageSize:  65536,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
}
