package fishaudio

import (
	"errors"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
		expected   string
	}{
		{
			name:       "401 error",
			statusCode: 401,
			message:    "Unauthorized",
			expected:   "HTTP 401: Unauthorized",
		},
		{
			name:       "500 error",
			statusCode: 500,
			message:    "Internal Server Error",
			expected:   "HTTP 500: Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &APIError{
				StatusCode: tt.statusCode,
				Message:    tt.message,
			}
			if got := err.Error(); got != tt.expected {
				t.Errorf("APIError.Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestWebSocketError_Error(t *testing.T) {
	msg := "connection failed"
	err := &WebSocketError{Message: msg}
	if got := err.Error(); got != msg {
		t.Errorf("WebSocketError.Error() = %q, want %q", got, msg)
	}
}

func TestNewAPIError(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		expectedType string
	}{
		{"401 returns AuthenticationError", 401, "*fishaudio.AuthenticationError"},
		{"403 returns PermissionError", 403, "*fishaudio.PermissionError"},
		{"404 returns NotFoundError", 404, "*fishaudio.NotFoundError"},
		{"422 returns ValidationError", 422, "*fishaudio.ValidationError"},
		{"429 returns RateLimitError", 429, "*fishaudio.RateLimitError"},
		{"500 returns ServerError", 500, "*fishaudio.ServerError"},
		{"502 returns ServerError", 502, "*fishaudio.ServerError"},
		{"503 returns ServerError", 503, "*fishaudio.ServerError"},
		{"400 returns APIError", 400, "*fishaudio.APIError"},
		{"418 returns APIError", 418, "*fishaudio.APIError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newAPIError(tt.statusCode, "test message", "test body")
			gotType := getTypeName(err)
			if gotType != tt.expectedType {
				t.Errorf("newAPIError(%d) type = %s, want %s", tt.statusCode, gotType, tt.expectedType)
			}
		})
	}
}

func TestNewAPIError_PreservesFields(t *testing.T) {
	err := newAPIError(401, "Unauthorized", `{"error": "invalid_token"}`)

	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatal("expected AuthenticationError")
	}

	if authErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", authErr.StatusCode)
	}
	if authErr.Message != "Unauthorized" {
		t.Errorf("Message = %q, want %q", authErr.Message, "Unauthorized")
	}
	if authErr.Body != `{"error": "invalid_token"}` {
		t.Errorf("Body = %q, want %q", authErr.Body, `{"error": "invalid_token"}`)
	}
}

func TestFishAudioError_Interface(t *testing.T) {
	// Verify all error types implement FishAudioError
	var _ FishAudioError = &APIError{}
	var _ FishAudioError = &AuthenticationError{}
	var _ FishAudioError = &PermissionError{}
	var _ FishAudioError = &NotFoundError{}
	var _ FishAudioError = &ValidationError{}
	var _ FishAudioError = &RateLimitError{}
	var _ FishAudioError = &ServerError{}
	var _ FishAudioError = &WebSocketError{}
}

// getTypeName returns the type name of an error for comparison
func getTypeName(err error) string {
	switch err.(type) {
	case *AuthenticationError:
		return "*fishaudio.AuthenticationError"
	case *PermissionError:
		return "*fishaudio.PermissionError"
	case *NotFoundError:
		return "*fishaudio.NotFoundError"
	case *ValidationError:
		return "*fishaudio.ValidationError"
	case *RateLimitError:
		return "*fishaudio.RateLimitError"
	case *ServerError:
		return "*fishaudio.ServerError"
	case *APIError:
		return "*fishaudio.APIError"
	default:
		return "unknown"
	}
}
