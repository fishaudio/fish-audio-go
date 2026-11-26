package fishaudio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

// refusingServer answers every request, WebSocket handshakes included, with a
// 401 that carries the API's diagnostic headers.
func refusingServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-fish-trace-id", "trace-123")
		w.Header().Set("x-fish-error-code", "invalid_api_key")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":401,"message":"Invalid Token"}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAPIError_HeaderFromHTTPError(t *testing.T) {
	server := refusingServer(t)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	_, err := client.TTS.Convert(context.Background(), &ConvertParams{Text: "hi"})

	// The README's pattern: *APIError matches a typed error such as 401.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*APIError) did not match %T: %v", err, err)
	}
	if got := apiErr.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("trace id = %q, want %q", got, "trace-123")
	}
	if got := apiErr.Header.Get("x-fish-error-code"); got != "invalid_api_key" {
		t.Errorf("error code = %q, want %q", got, "invalid_api_key")
	}
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Errorf("errors.As(*AuthenticationError) did not match %T", err)
	}
}

func TestAPIError_HeaderAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	_, err := client.Account.GetCredits(context.Background(), nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*APIError) did not match %T: %v", err, err)
	}
	if got := apiErr.Header.Get("x-fish-trace-id"); got != "" {
		t.Errorf("trace id = %q, want empty", got)
	}
	// An APIError built without a response has a nil Header; Get still works.
	if got := (&APIError{}).Header.Get("x-fish-trace-id"); got != "" {
		t.Errorf("nil Header.Get = %q, want empty", got)
	}
}

func TestStreamWebSocket_RefusedHandshake(t *testing.T) {
	server := refusingServer(t)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	textChan := make(chan string)
	close(textChan)
	_, err := client.TTS.StreamWebSocket(context.Background(), textChan, nil, nil)

	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("errors.As(*AuthenticationError) did not match %T: %v", err, err)
	}
	if authErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", authErr.StatusCode)
	}
	if authErr.Body != `{"status":401,"message":"Invalid Token"}` {
		t.Errorf("Body = %q", authErr.Body)
	}
	if got := authErr.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("trace id = %q, want %q", got, "trace-123")
	}
	// Callers that matched the dial error before still do.
	if !errors.Is(err, websocket.ErrBadHandshake) {
		t.Errorf("errors.Is(websocket.ErrBadHandshake) = false for %v", err)
	}
}

func TestStreamWebSocket_DialFailureWithoutResponse(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close() // nothing listens: the dial fails before any response

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	textChan := make(chan string)
	close(textChan)
	_, err := client.TTS.StreamWebSocket(context.Background(), textChan, nil, nil)

	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Errorf("a failed connection must not be an APIError: %v", err)
	}
}
