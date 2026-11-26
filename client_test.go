package fishaudio

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewClient_WithAPIKey(t *testing.T) {
	apiKey := "test-api-key-12345"
	client := NewClient(WithAPIKey(apiKey))

	if client.apiKey != apiKey {
		t.Errorf("apiKey = %q, want %q", client.apiKey, apiKey)
	}
}

func TestNewClient_EnvFallback(t *testing.T) {
	envKey := "env-api-key-67890"
	_ = os.Setenv("FISH_API_KEY", envKey)
	defer func() { _ = os.Unsetenv("FISH_API_KEY") }()

	client := NewClient()

	if client.apiKey != envKey {
		t.Errorf("apiKey = %q, want %q (from env)", client.apiKey, envKey)
	}
}

func TestNewClient_WithOptions(t *testing.T) {
	customURL := "https://custom.api.example.com"
	customTimeout := 60 * time.Second

	client := NewClient(
		WithAPIKey("test-key"),
		WithBaseURL(customURL),
		WithTimeout(customTimeout),
	)

	if client.baseURL != customURL {
		t.Errorf("baseURL = %q, want %q", client.baseURL, customURL)
	}
	if client.timeout != customTimeout {
		t.Errorf("timeout = %v, want %v", client.timeout, customTimeout)
	}
}

func TestNewClient_ServicesInitialized(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))

	if client.TTS == nil {
		t.Error("TTS service is nil")
	}
	if client.ASR == nil {
		t.Error("ASR service is nil")
	}
	if client.Voices == nil {
		t.Error("Voices service is nil")
	}
	if client.Account == nil {
		t.Error("Account service is nil")
	}
}

func TestNewClient_DefaultValues(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))

	if client.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", client.baseURL, DefaultBaseURL)
	}
	if client.timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", client.timeout, DefaultTimeout)
	}
}

func TestClient_Close(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	err := client.Close()
	if err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestClient_DoRequest_SetsHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-key")
		}

		userAgent := r.Header.Get("User-Agent")
		expected := "fish-audio/go/" + Version
		if userAgent != expected {
			t.Errorf("User-Agent = %q, want %q", userAgent, expected)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	resp, err := client.doRequest(context.Background(), http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestClient_DoRequest_WithBody(t *testing.T) {
	type testBody struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
		}

		var body testBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Failed to decode body: %v", err)
		}

		if body.Name != "test" || body.Value != 42 {
			t.Errorf("body = %+v, want {Name:test Value:42}", body)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	resp, err := client.doRequest(context.Background(), http.MethodPost, "/test",
		testBody{Name: "test", Value: 42}, nil)
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestClient_DoRequest_WithRequestOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify custom header
		custom := r.Header.Get("X-Custom-Header")
		if custom != "custom-value" {
			t.Errorf("X-Custom-Header = %q, want %q", custom, "custom-value")
		}

		// Verify query param
		param := r.URL.Query().Get("custom_param")
		if param != "param-value" {
			t.Errorf("custom_param = %q, want %q", param, "param-value")
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	opts := &RequestOptions{
		AdditionalHeaders: map[string]string{
			"X-Custom-Header": "custom-value",
		},
		AdditionalQueryParams: map[string]string{
			"custom_param": "param-value",
		},
	}

	resp, err := client.doRequest(context.Background(), http.MethodGet, "/test", nil, opts)
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestClient_DoRequest_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid_token"}`))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := client.doRequest(context.Background(), http.MethodGet, "/test", nil, nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var authErr *AuthenticationError
	if !containsError(err, &authErr) {
		t.Errorf("expected AuthenticationError, got %T", err)
	}
}

func TestClient_DoJSONRequest(t *testing.T) {
	type response struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response{ID: "123", Name: "test"})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	var result response
	err := client.doJSONRequest(context.Background(), http.MethodGet, "/test", nil, &result, nil)

	if err != nil {
		t.Fatalf("doJSONRequest() error = %v", err)
	}
	if result.ID != "123" {
		t.Errorf("ID = %q, want %q", result.ID, "123")
	}
	if result.Name != "test" {
		t.Errorf("Name = %q, want %q", result.Name, "test")
	}
}

func TestClient_DoRequest_MarshalError(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"), WithBaseURL("http://localhost"))
	// math.Inf produces a value that json.Marshal cannot handle
	body := map[string]interface{}{"bad": math.Inf(1)}
	_, err := client.doRequest(context.Background(), http.MethodPost, "/test", body, nil)
	if err == nil {
		t.Fatal("expected marshal error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to marshal") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to marshal")
	}
}

func TestClient_DoRequest_NetworkError(t *testing.T) {
	// Use a port that is not listening
	client := NewClient(WithAPIKey("test-key"), WithBaseURL("http://127.0.0.1:1"))
	_, err := client.doRequest(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "request failed")
	}
}

func TestClient_DoRequest_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.doRequest(ctx, http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestClient_DoJSONRequest_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	var result map[string]interface{}
	err := client.doJSONRequest(context.Background(), http.MethodGet, "/test", nil, &result, nil)
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to decode")
	}
}

func TestClient_DoRequest_MultipleErrorStatuses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errType    string
	}{
		{"forbidden", 403, "PermissionError"},
		{"not_found", 404, "NotFoundError"},
		{"validation", 422, "ValidationError"},
		{"rate_limit", 429, "RateLimitError"},
		{"server_error", 500, "ServerError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"error": "test"}`))
			}))
			defer server.Close()

			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
			_, err := client.doRequest(context.Background(), http.MethodGet, "/test", nil, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			switch tt.statusCode {
			case 403:
				var target *PermissionError
				if !containsError(err, &target) {
					t.Errorf("expected PermissionError, got %T: %v", err, err)
				}
			case 404:
				var target *NotFoundError
				if !containsError(err, &target) {
					t.Errorf("expected NotFoundError, got %T: %v", err, err)
				}
			case 422:
				var target *ValidationError
				if !containsError(err, &target) {
					t.Errorf("expected ValidationError, got %T: %v", err, err)
				}
			case 429:
				var target *RateLimitError
				if !containsError(err, &target) {
					t.Errorf("expected RateLimitError, got %T: %v", err, err)
				}
			case 500:
				var target *ServerError
				if !containsError(err, &target) {
					t.Errorf("expected ServerError, got %T: %v", err, err)
				}
			}
		})
	}
}

// containsError checks if err is of a specific type using errors.As pattern
func containsError[T error](err error, target *T) bool {
	for e := err; e != nil; {
		if _, ok := e.(T); ok {
			return true
		}
		if unwrapper, ok := e.(interface{ Unwrap() error }); ok {
			e = unwrapper.Unwrap()
		} else {
			break
		}
	}
	return false
}

// recordingTransport records each request it carries, then passes it on.
type recordingTransport struct {
	requests []string
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.requests = append(rt.requests, req.Method+" "+req.URL.Path)
	return http.DefaultTransport.RoundTrip(req)
}

func TestWithHTTPClient_CarriesEveryHTTPCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tts":
			_, _ = w.Write([]byte("audio"))
		case "/model":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	rt := &recordingTransport{}
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
		WithHTTPClient(&http.Client{Transport: rt}))
	ctx := context.Background()

	calls := []struct {
		want string
		call func() error
	}{
		{"GET /wallet/self/api-credit", func() error { _, err := client.Account.GetCredits(ctx, nil); return err }},
		{"GET /wallet/self/package", func() error { _, err := client.Account.GetPackage(ctx); return err }},
		{"GET /model", func() error { _, err := client.Voices.List(ctx, nil); return err }},
		{"GET /model/v1", func() error { _, err := client.Voices.Get(ctx, "v1"); return err }},
		{"POST /model", func() error {
			_, err := client.Voices.Create(ctx, &CreateVoiceParams{Title: "t", Voices: [][]byte{[]byte("a")}})
			return err
		}},
		{"PATCH /model/v1", func() error { return client.Voices.Update(ctx, "v1", &UpdateVoiceParams{Title: "t"}) }},
		{"DELETE /model/v1", func() error { return client.Voices.Delete(ctx, "v1") }},
		{"POST /v1/tts", func() error { _, err := client.TTS.Convert(ctx, &ConvertParams{Text: "hi"}); return err }},
		{"POST /v1/tts", func() error {
			s, err := client.TTS.Stream(ctx, &StreamParams{Text: "hi"})
			if err != nil {
				return err
			}
			_, err = s.Collect()
			return err
		}},
		{"POST /v1/asr", func() error { _, err := client.ASR.Transcribe(ctx, []byte("a"), nil); return err }},
	}

	for _, c := range calls {
		rt.requests = nil
		if err := c.call(); err != nil {
			t.Errorf("%s: error = %v", c.want, err)
			continue
		}
		got := rt.requests
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("requests through WithHTTPClient = %q, want [%q]", got, c.want)
		}
	}
}
