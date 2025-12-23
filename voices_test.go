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
		json.NewEncoder(w).Encode(PaginatedResponse[Voice]{
			Total: 0,
			Items: []Voice{},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
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
		json.NewEncoder(w).Encode(PaginatedResponse[Voice]{
			Total: 0,
			Items: []Voice{},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
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
		json.NewEncoder(w).Encode(PaginatedResponse[Voice]{
			Total: 2,
			Items: []Voice{
				{ID: "voice-1", Title: "Voice One"},
				{ID: "voice-2", Title: "Voice Two"},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
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
		json.NewEncoder(w).Encode(Voice{
			ID:    "voice-123",
			Title: "Test Voice",
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
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
	client := NewClient("test-key")

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
		json.NewEncoder(w).Encode(Voice{ID: "new-voice"})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
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

	client := NewClient("test-key", WithBaseURL(server.URL))
	err := client.Voices.Delete(context.Background(), "voice-123")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestVoicesService_Update_NilParams(t *testing.T) {
	client := NewClient("test-key")
	err := client.Voices.Update(context.Background(), "voice-123", nil)
	if err != nil {
		t.Errorf("Update(nil) should not error, got %v", err)
	}
}
