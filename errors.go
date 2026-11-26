package fishaudio

import (
	"fmt"
	"net/http"
)

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
	// Header holds the headers of the error response.
	Header http.Header
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

func (e *APIError) IsFishAudioError() {}

// AuthenticationError is raised when authentication fails (401).
type AuthenticationError struct {
	*APIError
}

// Unwrap lets errors.As match the embedded *APIError.
func (e *AuthenticationError) Unwrap() error { return e.APIError }

// PermissionError is raised when permission is denied (403).
type PermissionError struct {
	*APIError
}

// Unwrap lets errors.As match the embedded *APIError.
func (e *PermissionError) Unwrap() error { return e.APIError }

// NotFoundError is raised when a resource is not found (404).
type NotFoundError struct {
	*APIError
}

// Unwrap lets errors.As match the embedded *APIError.
func (e *NotFoundError) Unwrap() error { return e.APIError }

// RateLimitError is raised when rate limit is exceeded (429).
type RateLimitError struct {
	*APIError
}

// Unwrap lets errors.As match the embedded *APIError.
func (e *RateLimitError) Unwrap() error { return e.APIError }

// ValidationError is raised when request validation fails (422).
type ValidationError struct {
	*APIError
}

// Unwrap lets errors.As match the embedded *APIError.
func (e *ValidationError) Unwrap() error { return e.APIError }

// ServerError is raised when the server encounters an error (5xx).
type ServerError struct {
	*APIError
}

// Unwrap lets errors.As match the embedded *APIError.
func (e *ServerError) Unwrap() error { return e.APIError }

// WebSocketError is raised when WebSocket connection or streaming fails.
type WebSocketError struct {
	Message string
}

func (e *WebSocketError) Error() string {
	return e.Message
}

func (e *WebSocketError) IsFishAudioError() {}

// newAPIError creates the appropriate error type based on status code.
func newAPIError(statusCode int, message, body string, header http.Header) error {
	base := &APIError{
		StatusCode: statusCode,
		Message:    message,
		Body:       body,
		Header:     header,
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
