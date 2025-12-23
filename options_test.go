package fishaudio

import (
	"net/http"
	"testing"
	"time"
)

func TestWithBaseURL(t *testing.T) {
	customURL := "https://custom.api.example.com"
	client := NewClient("test-key", WithBaseURL(customURL))

	if client.baseURL != customURL {
		t.Errorf("WithBaseURL() baseURL = %q, want %q", client.baseURL, customURL)
	}
}

func TestWithHTTPClient(t *testing.T) {
	customClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	client := NewClient("test-key", WithHTTPClient(customClient))

	if client.httpClient != customClient {
		t.Error("WithHTTPClient() did not set custom HTTP client")
	}
}

func TestWithTimeout(t *testing.T) {
	customTimeout := 60 * time.Second
	client := NewClient("test-key", WithTimeout(customTimeout))

	if client.timeout != customTimeout {
		t.Errorf("WithTimeout() timeout = %v, want %v", client.timeout, customTimeout)
	}
	// Also verify HTTP client timeout is updated
	if client.httpClient.Timeout != customTimeout {
		t.Errorf("WithTimeout() httpClient.Timeout = %v, want %v", client.httpClient.Timeout, customTimeout)
	}
}

func TestDefaultWebSocketOptions(t *testing.T) {
	opts := DefaultWebSocketOptions()

	if opts.PingTimeout != 20*time.Second {
		t.Errorf("PingTimeout = %v, want %v", opts.PingTimeout, 20*time.Second)
	}
	if opts.PingInterval != 20*time.Second {
		t.Errorf("PingInterval = %v, want %v", opts.PingInterval, 20*time.Second)
	}
	if opts.MaxMessageSize != 65536 {
		t.Errorf("MaxMessageSize = %d, want %d", opts.MaxMessageSize, 65536)
	}
	if opts.ReadBufferSize != 1024 {
		t.Errorf("ReadBufferSize = %d, want %d", opts.ReadBufferSize, 1024)
	}
	if opts.WriteBufferSize != 1024 {
		t.Errorf("WriteBufferSize = %d, want %d", opts.WriteBufferSize, 1024)
	}
}

func TestRequestOptions_Fields(t *testing.T) {
	opts := RequestOptions{
		Timeout: 30 * time.Second,
		AdditionalHeaders: map[string]string{
			"X-Custom-Header": "value",
		},
		AdditionalQueryParams: map[string]string{
			"param": "value",
		},
	}

	if opts.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want %v", opts.Timeout, 30*time.Second)
	}
	if opts.AdditionalHeaders["X-Custom-Header"] != "value" {
		t.Error("AdditionalHeaders not set correctly")
	}
	if opts.AdditionalQueryParams["param"] != "value" {
		t.Error("AdditionalQueryParams not set correctly")
	}
}
