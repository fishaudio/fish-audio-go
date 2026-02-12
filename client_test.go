package fishaudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
