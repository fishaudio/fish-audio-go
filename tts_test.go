package fishaudio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTTSService_BuildRequest_Minimal(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	params := &StreamParams{
		Text: "Hello, world!",
	}

	req := service.buildRequest(params)

	if req.Text != "Hello, world!" {
		t.Errorf("Text = %q, want %q", req.Text, "Hello, world!")
	}
	if req.Prosody != nil {
		t.Error("Prosody should be nil for minimal params")
	}
}

func TestTTSService_BuildRequest_WithReferenceID(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	params := &StreamParams{
		Text:        "Hello",
		ReferenceID: "voice-123",
	}

	req := service.buildRequest(params)

	if req.ReferenceID != "voice-123" {
		t.Errorf("ReferenceID = %q, want %q", req.ReferenceID, "voice-123")
	}
}

func TestTTSService_BuildRequest_WithSpeed(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	params := &StreamParams{
		Text:  "Hello",
		Speed: 1.5,
	}

	req := service.buildRequest(params)

	if req.Prosody == nil {
		t.Fatal("Prosody should not be nil when speed is set")
	}
	if req.Prosody.Speed != 1.5 {
		t.Errorf("Prosody.Speed = %v, want %v", req.Prosody.Speed, 1.5)
	}
}

func TestTTSService_BuildRequest_WithFormat(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	params := &StreamParams{
		Text:   "Hello",
		Format: AudioFormatWAV,
	}

	req := service.buildRequest(params)

	if req.Format != AudioFormatWAV {
		t.Errorf("Format = %q, want %q", req.Format, AudioFormatWAV)
	}
}

func TestTTSService_BuildRequest_WithLatency(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	params := &StreamParams{
		Text:    "Hello",
		Latency: LatencyBalanced,
	}

	req := service.buildRequest(params)

	if req.Latency != LatencyBalanced {
		t.Errorf("Latency = %q, want %q", req.Latency, LatencyBalanced)
	}
}

func TestTTSService_BuildRequest_WithConfig(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	normalize := true
	params := &StreamParams{
		Text: "Hello",
		Config: &TTSConfig{
			Format:      AudioFormatOpus,
			SampleRate:  44100,
			MP3Bitrate:  192,
			OpusBitrate: 64,
			Normalize:   &normalize,
			ChunkLength: 250,
			Latency:     LatencyNormal,
			TopP:        0.8,
			Temperature: 0.9,
		},
	}

	req := service.buildRequest(params)

	if req.Format != AudioFormatOpus {
		t.Errorf("Format = %q, want %q", req.Format, AudioFormatOpus)
	}
	if req.SampleRate != 44100 {
		t.Errorf("SampleRate = %d, want %d", req.SampleRate, 44100)
	}
	if req.MP3Bitrate != 192 {
		t.Errorf("MP3Bitrate = %d, want %d", req.MP3Bitrate, 192)
	}
	if req.OpusBitrate != 64 {
		t.Errorf("OpusBitrate = %d, want %d", req.OpusBitrate, 64)
	}
	if req.Normalize == nil || *req.Normalize != true {
		t.Error("Normalize should be true")
	}
	if req.ChunkLength != 250 {
		t.Errorf("ChunkLength = %d, want %d", req.ChunkLength, 250)
	}
	if req.Latency != LatencyNormal {
		t.Errorf("Latency = %q, want %q", req.Latency, LatencyNormal)
	}
	if req.TopP != 0.8 {
		t.Errorf("TopP = %v, want %v", req.TopP, 0.8)
	}
	if req.Temperature != 0.9 {
		t.Errorf("Temperature = %v, want %v", req.Temperature, 0.9)
	}
}

func TestTTSService_BuildRequest_ConfigOverride(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	// Params should take precedence over config
	params := &StreamParams{
		Text:        "Hello",
		ReferenceID: "param-voice",
		Format:      AudioFormatMP3,
		Latency:     LatencyBalanced,
		Config: &TTSConfig{
			ReferenceID: "config-voice", // should be ignored
			Format:      AudioFormatWAV, // should be ignored
			Latency:     LatencyNormal,  // should be ignored
		},
	}

	req := service.buildRequest(params)

	if req.ReferenceID != "param-voice" {
		t.Errorf("ReferenceID = %q, want %q (from params)", req.ReferenceID, "param-voice")
	}
	if req.Format != AudioFormatMP3 {
		t.Errorf("Format = %q, want %q (from params)", req.Format, AudioFormatMP3)
	}
	if req.Latency != LatencyBalanced {
		t.Errorf("Latency = %q, want %q (from params)", req.Latency, LatencyBalanced)
	}
}

func TestTTSService_BuildRequest_ConfigFallback(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	// Config values should be used when params are empty
	params := &StreamParams{
		Text: "Hello",
		Config: &TTSConfig{
			ReferenceID: "config-voice",
			Format:      AudioFormatWAV,
			Latency:     LatencyNormal,
		},
	}

	req := service.buildRequest(params)

	if req.ReferenceID != "config-voice" {
		t.Errorf("ReferenceID = %q, want %q (from config)", req.ReferenceID, "config-voice")
	}
	if req.Format != AudioFormatWAV {
		t.Errorf("Format = %q, want %q (from config)", req.Format, AudioFormatWAV)
	}
	if req.Latency != LatencyNormal {
		t.Errorf("Latency = %q, want %q (from config)", req.Latency, LatencyNormal)
	}
}

func TestTTSService_BuildRequest_WithReferences(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	refs := []ReferenceAudio{
		{Audio: []byte("audio1"), Text: "text1"},
		{Audio: []byte("audio2"), Text: "text2"},
	}

	params := &StreamParams{
		Text:       "Hello",
		References: refs,
	}

	req := service.buildRequest(params)

	if len(req.References) != 2 {
		t.Fatalf("References length = %d, want %d", len(req.References), 2)
	}
	if string(req.References[0].Audio) != "audio1" {
		t.Errorf("References[0].Audio = %q, want %q", string(req.References[0].Audio), "audio1")
	}
}

func TestTTSService_BuildRequest_ConfigProsodyFallback(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"))
	service := client.TTS

	// Config prosody should be used when speed is not set
	params := &StreamParams{
		Text: "Hello",
		Config: &TTSConfig{
			Prosody: &Prosody{Speed: 0.8, Volume: 5.0},
		},
	}

	req := service.buildRequest(params)

	if req.Prosody == nil {
		t.Fatal("Prosody should not be nil")
	}
	if req.Prosody.Speed != 0.8 {
		t.Errorf("Prosody.Speed = %v, want %v", req.Prosody.Speed, 0.8)
	}
	if req.Prosody.Volume != 5.0 {
		t.Errorf("Prosody.Volume = %v, want %v", req.Prosody.Volume, 5.0)
	}
}

func TestTTSService_Stream(t *testing.T) {
	audioData := []byte("fake audio data")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/v1/tts" {
			t.Errorf("Path = %q, want %q", r.URL.Path, "/v1/tts")
		}

		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audioData)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	stream, err := client.TTS.Stream(context.Background(), &StreamParams{
		Text: "Hello",
	})

	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	data, err := stream.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if string(data) != string(audioData) {
		t.Errorf("audio data = %q, want %q", string(data), string(audioData))
	}
}

func TestTTSService_Convert(t *testing.T) {
	audioData := []byte("fake audio data for convert")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audioData)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	data, err := client.TTS.Convert(context.Background(), &ConvertParams{
		Text: "Hello",
	})

	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	if string(data) != string(audioData) {
		t.Errorf("audio data = %q, want %q", string(data), string(audioData))
	}
}
