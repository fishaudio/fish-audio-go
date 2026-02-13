package fishaudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoicesService_List_DefaultParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodGet)
		}

		// Check default query params
		query := r.URL.Query()
		if query.Get("page_size") != "10" {
			t.Errorf("page_size = %q, want %q", query.Get("page_size"), "10")
		}
		if query.Get("page_number") != "1" {
			t.Errorf("page_number = %q, want %q", query.Get("page_number"), "1")
		}
		if query.Get("sort_by") != "task_count" {
			t.Errorf("sort_by = %q, want %q", query.Get("sort_by"), "task_count")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PaginatedResponse[Voice]{
			Total: 0,
			Items: []Voice{},
		})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := client.Voices.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
}

func TestVoicesService_List_WithParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		if query.Get("page_size") != "20" {
			t.Errorf("page_size = %q, want %q", query.Get("page_size"), "20")
		}
		if query.Get("page_number") != "3" {
			t.Errorf("page_number = %q, want %q", query.Get("page_number"), "3")
		}
		if query.Get("title") != "Test Voice" {
			t.Errorf("title = %q, want %q", query.Get("title"), "Test Voice")
		}
		if query.Get("self") != "true" {
			t.Errorf("self = %q, want %q", query.Get("self"), "true")
		}
		if query.Get("author_id") != "author-123" {
			t.Errorf("author_id = %q, want %q", query.Get("author_id"), "author-123")
		}
		if query.Get("sort_by") != "created_at" {
			t.Errorf("sort_by = %q, want %q", query.Get("sort_by"), "created_at")
		}

		// Check multi-value params
		tags := query["tag"]
		if len(tags) != 2 || tags[0] != "english" || tags[1] != "female" {
			t.Errorf("tags = %v, want [english, female]", tags)
		}

		languages := query["language"]
		if len(languages) != 2 || languages[0] != "en" || languages[1] != "zh" {
			t.Errorf("languages = %v, want [en, zh]", languages)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PaginatedResponse[Voice]{
			Total: 0,
			Items: []Voice{},
		})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := client.Voices.List(context.Background(), &ListVoicesParams{
		PageSize:   20,
		PageNumber: 3,
		Title:      "Test Voice",
		Tags:       []string{"english", "female"},
		SelfOnly:   true,
		AuthorID:   "author-123",
		Language:   []string{"en", "zh"},
		SortBy:     "created_at",
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
}

func TestVoicesService_List_Response(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PaginatedResponse[Voice]{
			Total: 2,
			Items: []Voice{
				{ID: "voice-1", Title: "Voice One"},
				{ID: "voice-2", Title: "Voice Two"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	result, err := client.Voices.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if result.Total != 2 {
		t.Errorf("Total = %d, want %d", result.Total, 2)
	}
	if len(result.Items) != 2 {
		t.Errorf("Items length = %d, want %d", len(result.Items), 2)
	}
	if result.Items[0].ID != "voice-1" {
		t.Errorf("Items[0].ID = %q, want %q", result.Items[0].ID, "voice-1")
	}
}

func TestVoicesService_Get(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodGet)
		}
		if !strings.HasSuffix(r.URL.Path, "/voice-123") {
			t.Errorf("Path = %q, want suffix %q", r.URL.Path, "/voice-123")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Voice{
			ID:    "voice-123",
			Title: "Test Voice",
		})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	voice, err := client.Voices.Get(context.Background(), "voice-123")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if voice.ID != "voice-123" {
		t.Errorf("ID = %q, want %q", voice.ID, "voice-123")
	}
	if voice.Title != "Test Voice" {
		t.Errorf("Title = %q, want %q", voice.Title, "Test Voice")
	}
}

func TestVoicesService_Create_RequiresVoices(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))

	// Test with nil params
	_, err := client.Voices.Create(context.Background(), nil)
	if err == nil {
		t.Error("Create(nil) should return error")
	}

	// Test with empty voices
	_, err = client.Voices.Create(context.Background(), &CreateVoiceParams{
		Title:  "Test",
		Voices: [][]byte{},
	})
	if err == nil {
		t.Error("Create with empty voices should return error")
	}
}

func TestVoicesService_Create_Defaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPost)
		}

		// Parse multipart form
		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		// Check defaults
		if r.FormValue("visibility") != "private" {
			t.Errorf("visibility = %q, want %q", r.FormValue("visibility"), "private")
		}
		if r.FormValue("train_mode") != "fast" {
			t.Errorf("train_mode = %q, want %q", r.FormValue("train_mode"), "fast")
		}
		if r.FormValue("enhance_audio_quality") != "true" {
			t.Errorf("enhance_audio_quality = %q, want %q", r.FormValue("enhance_audio_quality"), "true")
		}
		if r.FormValue("type") != "tts" {
			t.Errorf("type = %q, want %q", r.FormValue("type"), "tts")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Voice{ID: "new-voice"})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := client.Voices.Create(context.Background(), &CreateVoiceParams{
		Title:  "Test Voice",
		Voices: [][]byte{[]byte("audio data")},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestVoicesService_Delete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodDelete)
		}
		if !strings.HasSuffix(r.URL.Path, "/voice-123") {
			t.Errorf("Path = %q, want suffix %q", r.URL.Path, "/voice-123")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	err := client.Voices.Delete(context.Background(), "voice-123")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestVoicesService_Update_NilParams(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	err := client.Voices.Update(context.Background(), "voice-123", nil)
	if err != nil {
		t.Errorf("Update(nil) should not error, got %v", err)
	}
}

func TestVoicesService_Create_AllFields(t *testing.T) {
	enhanceQuality := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPost)
		}

		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		// Check all form fields
		if r.FormValue("title") != "My Voice" {
			t.Errorf("title = %q, want %q", r.FormValue("title"), "My Voice")
		}
		if r.FormValue("description") != "A test voice" {
			t.Errorf("description = %q, want %q", r.FormValue("description"), "A test voice")
		}
		if r.FormValue("visibility") != "public" {
			t.Errorf("visibility = %q, want %q", r.FormValue("visibility"), "public")
		}
		if r.FormValue("texts") != "hello,world" {
			t.Errorf("texts = %q, want %q", r.FormValue("texts"), "hello,world")
		}
		if r.FormValue("tags") != "english,female" {
			t.Errorf("tags = %q, want %q", r.FormValue("tags"), "english,female")
		}
		if r.FormValue("enhance_audio_quality") != "false" {
			t.Errorf("enhance_audio_quality = %q, want %q", r.FormValue("enhance_audio_quality"), "false")
		}

		// Check voice file
		file, header, err := r.FormFile("voices")
		if err != nil {
			t.Fatalf("FormFile(voices) error = %v", err)
		}
		defer func() { _ = file.Close() }()

		if header.Filename != "voice_0.wav" {
			t.Errorf("voice filename = %q, want %q", header.Filename, "voice_0.wav")
		}

		// Check cover image
		cover, coverHeader, err := r.FormFile("cover_image")
		if err != nil {
			t.Fatalf("FormFile(cover_image) error = %v", err)
		}
		defer func() { _ = cover.Close() }()

		if coverHeader.Filename != "cover.png" {
			t.Errorf("cover filename = %q, want %q", coverHeader.Filename, "cover.png")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Voice{ID: "new-voice", Title: "My Voice"})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	voice, err := client.Voices.Create(context.Background(), &CreateVoiceParams{
		Title:               "My Voice",
		Description:         "A test voice",
		Visibility:          VisibilityPublic,
		Voices:              [][]byte{[]byte("audio data")},
		Texts:               []string{"hello", "world"},
		Tags:                []string{"english", "female"},
		CoverImage:          []byte("image data"),
		EnhanceAudioQuality: &enhanceQuality,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if voice.ID != "new-voice" {
		t.Errorf("voice.ID = %q, want %q", voice.ID, "new-voice")
	}
}

func TestVoicesService_Create_MultipleVoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		// Check multiple voice files
		files := r.MultipartForm.File["voices"]
		if len(files) != 2 {
			t.Fatalf("voices file count = %d, want %d", len(files), 2)
		}
		if files[0].Filename != "voice_0.wav" {
			t.Errorf("voices[0] filename = %q, want %q", files[0].Filename, "voice_0.wav")
		}
		if files[1].Filename != "voice_1.wav" {
			t.Errorf("voices[1] filename = %q, want %q", files[1].Filename, "voice_1.wav")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Voice{ID: "multi-voice"})
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	voice, err := client.Voices.Create(context.Background(), &CreateVoiceParams{
		Title:  "Multi Voice",
		Voices: [][]byte{[]byte("audio1"), []byte("audio2")},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if voice.ID != "multi-voice" {
		t.Errorf("voice.ID = %q, want %q", voice.ID, "multi-voice")
	}
}

func TestVoicesService_Create_ErrorResponse(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"unauthorized", 401},
		{"rate_limited", 429},
		{"server_error", 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"error": "test"}`))
			}))
			defer server.Close()

			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
			_, err := client.Voices.Create(context.Background(), &CreateVoiceParams{
				Title:  "Test",
				Voices: [][]byte{[]byte("audio")},
			})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestVoicesService_Update_AllFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPatch)
		}
		if !strings.HasSuffix(r.URL.Path, "/model/voice-123") {
			t.Errorf("Path = %q, want suffix %q", r.URL.Path, "/model/voice-123")
		}

		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		if r.FormValue("title") != "Updated Title" {
			t.Errorf("title = %q, want %q", r.FormValue("title"), "Updated Title")
		}
		if r.FormValue("description") != "Updated desc" {
			t.Errorf("description = %q, want %q", r.FormValue("description"), "Updated desc")
		}
		if r.FormValue("visibility") != "public" {
			t.Errorf("visibility = %q, want %q", r.FormValue("visibility"), "public")
		}
		if r.FormValue("tags") != "tag1,tag2" {
			t.Errorf("tags = %q, want %q", r.FormValue("tags"), "tag1,tag2")
		}

		// Check cover image
		cover, _, err := r.FormFile("cover_image")
		if err != nil {
			t.Fatalf("FormFile(cover_image) error = %v", err)
		}
		defer func() { _ = cover.Close() }()

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	err := client.Voices.Update(context.Background(), "voice-123", &UpdateVoiceParams{
		Title:       "Updated Title",
		Description: "Updated desc",
		Visibility:  VisibilityPublic,
		Tags:        []string{"tag1", "tag2"},
		CoverImage:  []byte("new image"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestVoicesService_Update_PartialFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm error = %v", err)
		}

		// Only title should be present
		if r.FormValue("title") != "New Title" {
			t.Errorf("title = %q, want %q", r.FormValue("title"), "New Title")
		}
		// Description should not be present (empty means not set)
		if r.FormValue("description") != "" {
			t.Errorf("description = %q, want empty", r.FormValue("description"))
		}
		if r.FormValue("visibility") != "" {
			t.Errorf("visibility = %q, want empty", r.FormValue("visibility"))
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	err := client.Voices.Update(context.Background(), "voice-123", &UpdateVoiceParams{
		Title: "New Title",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestVoicesService_Update_ErrorResponse(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"unauthorized", 401},
		{"rate_limited", 429},
		{"server_error", 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"error": "test"}`))
			}))
			defer server.Close()

			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
			err := client.Voices.Update(context.Background(), "voice-123", &UpdateVoiceParams{
				Title: "Test",
			})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
