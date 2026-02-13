package fishaudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestASRService_Transcribe_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/v1/asr" {
			t.Errorf("Path = %q, want %q", r.URL.Path, "/v1/asr")
		}

		// Verify auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-key")
		}

		// Verify content type is multipart
		ct := r.Header.Get("Content-Type")
		if ct == "" || len(ct) < 19 {
			t.Errorf("Content-Type should be multipart/form-data, got %q", ct)
		}

		// Parse multipart form
		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		// Verify audio file
		file, header, err := r.FormFile("audio")
		if err != nil {
			t.Fatalf("FormFile(audio) error = %v", err)
		}
		defer func() { _ = file.Close() }()

		if header.Filename != "audio.mp3" {
			t.Errorf("audio filename = %q, want %q", header.Filename, "audio.mp3")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ASRResponse{
			Text:     "Hello world",
			Duration: 1500,
		})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	result, err := client.ASR.Transcribe(context.Background(), []byte("fake audio"), nil)
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}

	if result.Text != "Hello world" {
		t.Errorf("Text = %q, want %q", result.Text, "Hello world")
	}
	if result.Duration != 1500 {
		t.Errorf("Duration = %v, want %v", result.Duration, 1500)
	}
}

func TestASRService_Transcribe_WithLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		lang := r.FormValue("language")
		if lang != "en" {
			t.Errorf("language = %q, want %q", lang, "en")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ASRResponse{Text: "Hello"})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := client.ASR.Transcribe(context.Background(), []byte("audio"), &TranscribeParams{
		Language: "en",
	})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
}

func TestASRService_Transcribe_IgnoreTimestamps(t *testing.T) {
	tests := []struct {
		name              string
		includeTimestamps *bool
		wantIgnore        string
	}{
		{
			name:       "default (nil) includes timestamps",
			wantIgnore: "false",
		},
		{
			name:              "explicit true includes timestamps",
			includeTimestamps: boolPtr(true),
			wantIgnore:        "false",
		},
		{
			name:              "explicit false ignores timestamps",
			includeTimestamps: boolPtr(false),
			wantIgnore:        "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				err := r.ParseMultipartForm(10 << 20)
				if err != nil {
					t.Fatalf("ParseMultipartForm error = %v", err)
				}

				got := r.FormValue("ignore_timestamps")
				if got != tt.wantIgnore {
					t.Errorf("ignore_timestamps = %q, want %q", got, tt.wantIgnore)
				}

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(ASRResponse{Text: "ok"})
			}))
			defer server.Close()

			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
			_, err := client.ASR.Transcribe(context.Background(), []byte("audio"), &TranscribeParams{
				IncludeTimestamps: tt.includeTimestamps,
			})
			if err != nil {
				t.Fatalf("Transcribe() error = %v", err)
			}
		})
	}
}

func TestASRService_Transcribe_NilParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		// With nil params, language should be absent
		if r.FormValue("language") != "" {
			t.Errorf("language should be empty for nil params, got %q", r.FormValue("language"))
		}
		// ignore_timestamps defaults to false (include timestamps by default)
		if r.FormValue("ignore_timestamps") != "false" {
			t.Errorf("ignore_timestamps = %q, want %q", r.FormValue("ignore_timestamps"), "false")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ASRResponse{Text: "test"})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	result, err := client.ASR.Transcribe(context.Background(), []byte("audio"), nil)
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.Text != "test" {
		t.Errorf("Text = %q, want %q", result.Text, "test")
	}
}

func TestASRService_Transcribe_WithSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ASRResponse{
			Text:     "Hello world. How are you?",
			Duration: 3000,
			Segments: []ASRSegment{
				{Text: "Hello world.", Start: 0.0, End: 1.5},
				{Text: "How are you?", Start: 1.5, End: 3.0},
			},
		})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	result, err := client.ASR.Transcribe(context.Background(), []byte("audio"), nil)
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}

	if len(result.Segments) != 2 {
		t.Fatalf("Segments length = %d, want %d", len(result.Segments), 2)
	}
	if result.Segments[0].Text != "Hello world." {
		t.Errorf("Segments[0].Text = %q, want %q", result.Segments[0].Text, "Hello world.")
	}
	if result.Segments[0].Start != 0.0 {
		t.Errorf("Segments[0].Start = %v, want %v", result.Segments[0].Start, 0.0)
	}
	if result.Segments[0].End != 1.5 {
		t.Errorf("Segments[0].End = %v, want %v", result.Segments[0].End, 1.5)
	}
	if result.Segments[1].Text != "How are you?" {
		t.Errorf("Segments[1].Text = %q, want %q", result.Segments[1].Text, "How are you?")
	}
}

func TestASRService_Transcribe_ErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errType    string
	}{
		{"unauthorized", 401, "AuthenticationError"},
		{"rate_limited", 429, "RateLimitError"},
		{"server_error", 500, "ServerError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"error": "test error"}`))
			}))
			defer server.Close()

			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
			_, err := client.ASR.Transcribe(context.Background(), []byte("audio"), nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			switch tt.statusCode {
			case 401:
				var target *AuthenticationError
				if !containsError(err, &target) {
					t.Errorf("expected AuthenticationError, got %T: %v", err, err)
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

func TestASRService_Transcribe_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ASRResponse{Text: "should not reach"})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.ASR.Transcribe(ctx, []byte("audio"), nil)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}
