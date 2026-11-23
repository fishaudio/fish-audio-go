package fishaudio

import "fmt"

// FishAudioError is the base interface for all Fish Audio SDK errors.
type FishAudioError interface {
	error
	IsFishAudioError()
}

// APIError is raised when the API returns an error response.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

func (e *APIError) IsFishAudioError() {}

// AuthenticationError is raised when authentication fails (401).
type AuthenticationError struct {
	*APIError
}

// PermissionError is raised when permission is denied (403).
type PermissionError struct {
	*APIError
}

// NotFoundError is raised when a resource is not found (404).
type NotFoundError struct {
	*APIError
}

// RateLimitError is raised when rate limit is exceeded (429).
type RateLimitError struct {
	*APIError
}

// ValidationError is raised when request validation fails (422).
type ValidationError struct {
	*APIError
}

// ServerError is raised when the server encounters an error (5xx).
type ServerError struct {
	*APIError
}

// WebSocketError is raised when WebSocket connection or streaming fails.
type WebSocketError struct {
	Message string
	// Err is the underlying error, if any.
	Err error
}

func (e *WebSocketError) Error() string {
	return e.Message
}

// Unwrap returns the underlying error, if any.
func (e *WebSocketError) Unwrap() error { return e.Err }

func (e *WebSocketError) IsFishAudioError() {}

// ASRRealtimeError is returned when the server ends a realtime speech-to-text
// session abnormally: with an error it could not recover from, or by closing
// the WebSocket with a code other than 1000.
type ASRRealtimeError struct {
	// Message is a short English description, for humans.
	Message string
	// ErrorType is the server's error type, such as "invalid_config",
	// "idle_timeout" or "server_restarting". Empty when the connection closed
	// without an error message.
	ErrorType string
	// Code is the HTTP-style status of the server's error message, or 0.
	Code int
	// RequestID identifies the session to Fish Audio support.
	RequestID string
	// CloseCode is the WebSocket close code, such as 4400 or 1012, or 0 when
	// the connection did not close with one.
	CloseCode int
}

func (e *ASRRealtimeError) Error() string {
	msg := "realtime ASR session failed"
	if e.ErrorType != "" {
		msg += " (" + e.ErrorType + ")"
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.CloseCode != 0 {
		msg += fmt.Sprintf(" [close code %d]", e.CloseCode)
	}
	return msg
}

func (e *ASRRealtimeError) IsFishAudioError() {}

// newAPIError creates the appropriate error type based on status code.
func newAPIError(statusCode int, message, body string) error {
	base := &APIError{
		StatusCode: statusCode,
		Message:    message,
		Body:       body,
	}

	switch statusCode {
	case 401:
		return &AuthenticationError{APIError: base}
	case 403:
		return &PermissionError{APIError: base}
	case 404:
		return &NotFoundError{APIError: base}
	case 422:
		return &ValidationError{APIError: base}
	case 429:
		return &RateLimitError{APIError: base}
	default:
		if statusCode >= 500 {
			return &ServerError{APIError: base}
		}
		return base
	}
}
