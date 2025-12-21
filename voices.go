package fishaudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sample represents a sample audio for a voice model.
type Sample struct {
	Title  string `json:"title"`
	Text   string `json:"text"`
	TaskID string `json:"task_id"`
	Audio  string `json:"audio"`
}

// Author represents voice model author information.
type Author struct {
	ID       string `json:"_id"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// Voice represents a voice model.
type Voice struct {
	ID             string     `json:"_id"`
	Type           string     `json:"type"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	CoverImage     string     `json:"cover_image"`
	TrainMode      TrainMode  `json:"train_mode"`
	State          ModelState `json:"state"`
	Tags           []string   `json:"tags"`
	Samples        []Sample   `json:"samples"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Languages      []string   `json:"languages"`
	Visibility     Visibility `json:"visibility"`
	LockVisibility bool       `json:"lock_visibility"`
	LikeCount      int        `json:"like_count"`
	MarkCount      int        `json:"mark_count"`
	SharedCount    int        `json:"shared_count"`
	TaskCount      int        `json:"task_count"`
	Liked          bool       `json:"liked"`
	Marked         bool       `json:"marked"`
	Author         Author     `json:"author"`
}

// ListVoicesParams contains parameters for listing voices.
type ListVoicesParams struct {
	// PageSize is the number of results per page. Default: 10.
	PageSize int
	// PageNumber is the page number (1-indexed). Default: 1.
	PageNumber int
	// Title filters by title.
	Title string
	// Tags filters by tags.
	Tags []string
	// SelfOnly returns only the user's own voices.
	SelfOnly bool
	// AuthorID filters by author ID.
	AuthorID string
	// Language filters by language(s).
	Language []string
	// TitleLanguage filters by title language(s).
	TitleLanguage []string
	// SortBy is the sort field. Options: "task_count", "created_at". Default: "task_count".
	SortBy string
}

// CreateVoiceParams contains parameters for creating a voice.
type CreateVoiceParams struct {
	// Title is the voice model name (required).
	Title string
	// Voices is a list of audio file bytes for training (required).
	Voices [][]byte
	// Description is the voice description.
	Description string
	// Texts are transcripts for voice samples.
	Texts []string
	// Tags are tags for categorization.
	Tags []string
	// CoverImage is the cover image bytes.
	CoverImage []byte
	// Visibility is the visibility setting. Default: "private".
	Visibility Visibility
	// TrainMode is the training mode. Default: "fast".
	TrainMode TrainMode
	// EnhanceAudioQuality indicates whether to enhance audio quality. Default: true.
	EnhanceAudioQuality *bool
}

// UpdateVoiceParams contains parameters for updating a voice.
type UpdateVoiceParams struct {
	// Title is the new title.
	Title string
	// Description is the new description.
	Description string
	// CoverImage is the new cover image bytes.
	CoverImage []byte
	// Visibility is the new visibility setting.
	Visibility Visibility
	// Tags are the new tags.
	Tags []string
}

// VoicesService provides voice management operations.
type VoicesService struct {
	client *Client
}

// List returns available voices/models.
func (s *VoicesService) List(ctx context.Context, params *ListVoicesParams) (*PaginatedResponse[Voice], error) {
	if params == nil {
		params = &ListVoicesParams{}
	}

	// Build query parameters
	query := url.Values{}

	pageSize := params.PageSize
	if pageSize == 0 {
		pageSize = 10
	}
	query.Set("page_size", strconv.Itoa(pageSize))

	pageNumber := params.PageNumber
	if pageNumber == 0 {
		pageNumber = 1
	}
	query.Set("page_number", strconv.Itoa(pageNumber))

	if params.Title != "" {
		query.Set("title", params.Title)
	}
	if len(params.Tags) > 0 {
		for _, tag := range params.Tags {
			query.Add("tag", tag)
		}
	}
	if params.SelfOnly {
		query.Set("self", "true")
	}
	if params.AuthorID != "" {
		query.Set("author_id", params.AuthorID)
	}
	if len(params.Language) > 0 {
		for _, lang := range params.Language {
			query.Add("language", lang)
		}
	}
	if len(params.TitleLanguage) > 0 {
		for _, lang := range params.TitleLanguage {
			query.Add("title_language", lang)
		}
	}

	sortBy := params.SortBy
	if sortBy == "" {
		sortBy = "task_count"
	}
	query.Set("sort_by", sortBy)

	// Make request
	path := "/model?" + query.Encode()
	var result PaginatedResponse[Voice]
	if err := s.client.doJSONRequest(ctx, http.MethodGet, path, nil, &result, nil); err != nil {
		return nil, err
	}

	return &result, nil
}

// Get returns a voice by ID.
func (s *VoicesService) Get(ctx context.Context, voiceID string) (*Voice, error) {
	var result Voice
	if err := s.client.doJSONRequest(ctx, http.MethodGet, "/model/"+voiceID, nil, &result, nil); err != nil {
		return nil, err
	}
	return &result, nil
}

// Create creates/clones a new voice.
func (s *VoicesService) Create(ctx context.Context, params *CreateVoiceParams) (*Voice, error) {
	if params == nil || len(params.Voices) == 0 {
		return nil, fmt.Errorf("voices are required")
	}

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add title
	if err := writer.WriteField("title", params.Title); err != nil {
		return nil, err
	}

	// Add description
	if params.Description != "" {
		if err := writer.WriteField("description", params.Description); err != nil {
			return nil, err
		}
	}

	// Add visibility
	visibility := params.Visibility
	if visibility == "" {
		visibility = VisibilityPrivate
	}
	if err := writer.WriteField("visibility", string(visibility)); err != nil {
		return nil, err
	}

	// Add type
	if err := writer.WriteField("type", "tts"); err != nil {
		return nil, err
	}

	// Add train_mode
	trainMode := params.TrainMode
	if trainMode == "" {
		trainMode = TrainModeFast
	}
	if err := writer.WriteField("train_mode", string(trainMode)); err != nil {
		return nil, err
	}

	// Add enhance_audio_quality
	enhanceQuality := true
	if params.EnhanceAudioQuality != nil {
		enhanceQuality = *params.EnhanceAudioQuality
	}
	if err := writer.WriteField("enhance_audio_quality", strconv.FormatBool(enhanceQuality)); err != nil {
		return nil, err
	}

	// Add texts
	if len(params.Texts) > 0 {
		if err := writer.WriteField("texts", strings.Join(params.Texts, ",")); err != nil {
			return nil, err
		}
	}

	// Add tags
	if len(params.Tags) > 0 {
		if err := writer.WriteField("tags", strings.Join(params.Tags, ",")); err != nil {
			return nil, err
		}
	}

	// Add voice files
	for i, voice := range params.Voices {
		part, err := writer.CreateFormFile("voices", fmt.Sprintf("voice_%d.wav", i))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(voice); err != nil {
			return nil, err
		}
	}

	// Add cover image
	if len(params.CoverImage) > 0 {
		part, err := writer.CreateFormFile("cover_image", "cover.png")
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(params.CoverImage); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	// Create request
	urlStr := s.client.baseURL + "/model"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, &buf)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+s.client.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "fish-audio/go/"+Version)

	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, newAPIError(resp.StatusCode, resp.Status, string(bodyBytes))
	}

	var result Voice
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// Update updates voice metadata.
func (s *VoicesService) Update(ctx context.Context, voiceID string, params *UpdateVoiceParams) error {
	if params == nil {
		return nil
	}

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if params.Title != "" {
		if err := writer.WriteField("title", params.Title); err != nil {
			return err
		}
	}

	if params.Description != "" {
		if err := writer.WriteField("description", params.Description); err != nil {
			return err
		}
	}

	if params.Visibility != "" {
		if err := writer.WriteField("visibility", string(params.Visibility)); err != nil {
			return err
		}
	}

	if len(params.Tags) > 0 {
		if err := writer.WriteField("tags", strings.Join(params.Tags, ",")); err != nil {
			return err
		}
	}

	if len(params.CoverImage) > 0 {
		part, err := writer.CreateFormFile("cover_image", "cover.png")
		if err != nil {
			return err
		}
		if _, err := part.Write(params.CoverImage); err != nil {
			return err
		}
	}

	if err := writer.Close(); err != nil {
		return err
	}

	// Create request
	urlStr := s.client.baseURL + "/model/" + voiceID
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, urlStr, &buf)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+s.client.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "fish-audio/go/"+Version)

	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return newAPIError(resp.StatusCode, resp.Status, string(bodyBytes))
	}

	return nil
}

// Delete deletes a voice.
func (s *VoicesService) Delete(ctx context.Context, voiceID string) error {
	resp, err := s.client.doRequest(ctx, http.MethodDelete, "/model/"+voiceID, nil, nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}
