package fishaudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// ASRSegment represents a timestamped segment of transcribed text.
type ASRSegment struct {
	// Text is the transcribed text for this segment.
	Text string `json:"text"`
	// Start is the segment start time in seconds.
	Start float64 `json:"start"`
	// End is the segment end time in seconds.
	End float64 `json:"end"`
}

// ASRResponse contains the result of speech-to-text transcription.
type ASRResponse struct {
	// Text is the complete transcription of the audio.
	Text string `json:"text"`
	// Duration is the total audio duration in milliseconds.
	Duration float64 `json:"duration"`
	// Segments contains timestamped text segments.
	Segments []ASRSegment `json:"segments"`
}

// TranscribeParams contains parameters for ASR transcription.
type TranscribeParams struct {
	// Language is the language code (e.g., "en", "zh"). Auto-detected if empty.
	Language string
	// IncludeTimestamps indicates whether to include timestamp information. Default: true.
	IncludeTimestamps *bool
}

// ASRService provides speech-to-text operations.
type ASRService struct {
	client *Client
}

// Transcribe converts audio to text.
//
// Example:
//
//	audio, _ := os.ReadFile("audio.mp3")
//	result, err := client.ASR.Transcribe(ctx, audio, &fishaudio.TranscribeParams{
//	    Language: "en",
//	})
//	fmt.Println(result.Text)
func (s *ASRService) Transcribe(ctx context.Context, audio []byte, params *TranscribeParams) (*ASRResponse, error) {
	if params == nil {
		params = &TranscribeParams{}
	}

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add audio file
	part, err := writer.CreateFormFile("audio", "audio.mp3")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return nil, fmt.Errorf("failed to write audio: %w", err)
	}

	// Add language if specified
	if params.Language != "" {
		if err := writer.WriteField("language", params.Language); err != nil {
			return nil, fmt.Errorf("failed to write language: %w", err)
		}
	}

	// Add ignore_timestamps field
	includeTimestamps := true
	if params.IncludeTimestamps != nil {
		includeTimestamps = *params.IncludeTimestamps
	}
	if err := writer.WriteField("ignore_timestamps", fmt.Sprintf("%t", !includeTimestamps)); err != nil {
		return nil, fmt.Errorf("failed to write ignore_timestamps: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	// Create request
	url := s.client.baseURL + "/v1/asr"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.client.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "fish-audio/go/"+Version)

	// Execute request
	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, newAPIError(resp.StatusCode, resp.Status, string(bodyBytes))
	}

	// Parse response
	var result ASRResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}
