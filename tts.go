package fishaudio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// ReferenceAudio contains reference audio for voice cloning.
type ReferenceAudio struct {
	// Audio is the audio file bytes for the reference sample.
	Audio []byte `json:"audio" msgpack:"audio"`
	// Text is the transcription of what is spoken in the reference audio.
	Text string `json:"text" msgpack:"text"`
}

// Prosody contains speech prosody settings (speed and volume).
type Prosody struct {
	// Speed is the speech speed multiplier. Range: 0.5-2.0. Default: 1.0.
	Speed float64 `json:"speed,omitempty" msgpack:"speed,omitempty"`
	// Volume is the volume adjustment in decibels. Range: -20.0 to 20.0. Default: 0.0.
	Volume float64 `json:"volume,omitempty" msgpack:"volume,omitempty"`
}

// TTSConfig is reusable configuration for text-to-speech requests.
type TTSConfig struct {
	// Model is the TTS model to use. Options: "s1", "speech-1.6", "speech-1.5". Default: "s1".
	Model Model `json:"model,omitempty"`
	// Format is the audio output format. Options: "mp3", "wav", "pcm", "opus". Default: "mp3".
	Format AudioFormat `json:"format,omitempty"`
	// SampleRate is the audio sample rate in Hz.
	SampleRate int `json:"sample_rate,omitempty"`
	// MP3Bitrate is the MP3 bitrate in kbps. Options: 64, 128, 192. Default: 128.
	MP3Bitrate int `json:"mp3_bitrate,omitempty"`
	// OpusBitrate is the Opus bitrate in kbps. Options: -1000, 24, 32, 48, 64. Default: 32.
	OpusBitrate int `json:"opus_bitrate,omitempty"`
	// Normalize indicates whether to normalize/clean the input text. Default: true.
	Normalize *bool `json:"normalize,omitempty"`
	// ChunkLength is the characters per generation chunk. Range: 100-300. Default: 200.
	ChunkLength int `json:"chunk_length,omitempty"`
	// Latency is the generation mode. Options: "normal", "balanced". Default: "balanced".
	Latency LatencyMode `json:"latency,omitempty"`
	// ReferenceID is the voice model ID from fish.audio.
	ReferenceID string `json:"reference_id,omitempty"`
	// References is a list of reference audio samples for instant voice cloning.
	References []ReferenceAudio `json:"references,omitempty"`
	// Prosody contains speech speed and volume settings.
	Prosody *Prosody `json:"prosody,omitempty"`
	// TopP is the nucleus sampling parameter. Range: 0.0-1.0. Default: 0.7.
	TopP float64 `json:"top_p,omitempty"`
	// Temperature is the randomness in generation. Range: 0.0-1.0. Default: 0.7.
	Temperature float64 `json:"temperature,omitempty"`
}

// ConvertParams contains parameters for TTS conversion.
type ConvertParams struct {
	// Text is the text to synthesize into speech (required).
	Text string `json:"text"`
	// Model is the TTS model to use. Options: "s1", "speech-1.6", "speech-1.5". Default: "s1".
	Model Model `json:"model,omitempty"`
	// ReferenceID is the voice model ID to use.
	ReferenceID string `json:"reference_id,omitempty"`
	// References is a list of reference audio for voice cloning.
	References []ReferenceAudio `json:"references,omitempty"`
	// Format is the audio output format.
	Format AudioFormat `json:"format,omitempty"`
	// Latency is the generation mode.
	Latency LatencyMode `json:"latency,omitempty"`
	// Speed is a shorthand for setting prosody speed (0.5-2.0).
	Speed float64 `json:"-"`
	// Config provides additional TTS configuration.
	Config *TTSConfig `json:"-"`
}

// StreamParams contains parameters for TTS streaming.
type StreamParams struct {
	// Text is the text to synthesize into speech (required).
	Text string `json:"text"`
	// Model is the TTS model to use. Options: "s1", "speech-1.6", "speech-1.5". Default: "s1".
	Model Model `json:"-"`
	// ReferenceID is the voice model ID to use.
	ReferenceID string `json:"reference_id,omitempty"`
	// References is a list of reference audio for voice cloning.
	References []ReferenceAudio `json:"references,omitempty"`
	// Format is the audio output format.
	Format AudioFormat `json:"format,omitempty"`
	// Latency is the generation mode.
	Latency LatencyMode `json:"latency,omitempty"`
	// Speed is a shorthand for setting prosody speed (0.5-2.0).
	Speed float64 `json:"-"`
	// Config provides additional TTS configuration.
	Config *TTSConfig `json:"-"`
}

// ttsRequest is the internal API request structure.
type ttsRequest struct {
	Text        string           `json:"text" msgpack:"text"`
	ChunkLength int              `json:"chunk_length,omitempty" msgpack:"chunk_length,omitempty"`
	Format      AudioFormat      `json:"format,omitempty" msgpack:"format,omitempty"`
	SampleRate  int              `json:"sample_rate,omitempty" msgpack:"sample_rate,omitempty"`
	MP3Bitrate  int              `json:"mp3_bitrate,omitempty" msgpack:"mp3_bitrate,omitempty"`
	OpusBitrate int              `json:"opus_bitrate,omitempty" msgpack:"opus_bitrate,omitempty"`
	References  []ReferenceAudio `json:"references,omitempty" msgpack:"references,omitempty"`
	ReferenceID string           `json:"reference_id,omitempty" msgpack:"reference_id,omitempty"`
	Normalize   *bool            `json:"normalize,omitempty" msgpack:"normalize,omitempty"`
	Latency     LatencyMode      `json:"latency,omitempty" msgpack:"latency,omitempty"`
	Prosody     *Prosody         `json:"prosody,omitempty" msgpack:"prosody,omitempty"`
	TopP        float64          `json:"top_p,omitempty" msgpack:"top_p,omitempty"`
	Temperature float64          `json:"temperature,omitempty" msgpack:"temperature,omitempty"`
}

// TTSService provides text-to-speech operations.
type TTSService struct {
	client *Client
}

// Convert generates speech from text and returns the complete audio.
func (s *TTSService) Convert(ctx context.Context, params *ConvertParams) ([]byte, error) {
	stream, err := s.Stream(ctx, &StreamParams{
		Text:        params.Text,
		Model:       params.Model,
		ReferenceID: params.ReferenceID,
		References:  params.References,
		Format:      params.Format,
		Latency:     params.Latency,
		Speed:       params.Speed,
		Config:      params.Config,
	})
	if err != nil {
		return nil, err
	}
	return stream.Collect()
}

// Stream generates speech from text and returns an audio stream.
func (s *TTSService) Stream(ctx context.Context, params *StreamParams) (*AudioStream, error) {
	req := s.buildRequest(params)

	// Build request options with model header
	var opts *RequestOptions
	model := s.getModel(params)
	if model != "" {
		opts = &RequestOptions{
			AdditionalHeaders: map[string]string{"model": string(model)},
		}
	}

	resp, err := s.client.doRequest(ctx, http.MethodPost, "/v1/tts", req, opts)
	if err != nil {
		return nil, err
	}

	return newAudioStream(resp), nil
}

// getModel returns the model to use, checking params then config.
func (s *TTSService) getModel(params *StreamParams) Model {
	if params.Model != "" {
		return params.Model
	}
	if params.Config != nil && params.Config.Model != "" {
		return params.Config.Model
	}
	return ""
}

// buildRequest constructs the API request from params.
func (s *TTSService) buildRequest(params *StreamParams) *ttsRequest {
	req := &ttsRequest{
		Text:        params.Text,
		ReferenceID: params.ReferenceID,
		References:  params.References,
		Format:      params.Format,
		Latency:     params.Latency,
	}

	// Apply speed as prosody
	if params.Speed != 0 {
		req.Prosody = &Prosody{Speed: params.Speed}
	}

	// Apply config overrides
	if params.Config != nil {
		cfg := params.Config
		if cfg.Format != "" && req.Format == "" {
			req.Format = cfg.Format
		}
		if cfg.SampleRate != 0 {
			req.SampleRate = cfg.SampleRate
		}
		if cfg.MP3Bitrate != 0 {
			req.MP3Bitrate = cfg.MP3Bitrate
		}
		if cfg.OpusBitrate != 0 {
			req.OpusBitrate = cfg.OpusBitrate
		}
		if cfg.Normalize != nil {
			req.Normalize = cfg.Normalize
		}
		if cfg.ChunkLength != 0 {
			req.ChunkLength = cfg.ChunkLength
		}
		if cfg.Latency != "" && req.Latency == "" {
			req.Latency = cfg.Latency
		}
		if cfg.ReferenceID != "" && req.ReferenceID == "" {
			req.ReferenceID = cfg.ReferenceID
		}
		if len(cfg.References) > 0 && len(req.References) == 0 {
			req.References = cfg.References
		}
		if cfg.Prosody != nil && req.Prosody == nil {
			req.Prosody = cfg.Prosody
		}
		if cfg.TopP != 0 {
			req.TopP = cfg.TopP
		}
		if cfg.Temperature != 0 {
			req.Temperature = cfg.Temperature
		}
	}

	return req
}

// WebSocket event types for streaming TTS.

// startEvent initiates a TTS WebSocket streaming session.
type startEvent struct {
	Event   string      `msgpack:"event"`
	Request *ttsRequest `msgpack:"request"`
}

// textEvent sends a text chunk for synthesis.
type textEvent struct {
	Event string `msgpack:"event"`
	Text  string `msgpack:"text"`
}

// closeEvent ends the streaming session.
type closeEvent struct {
	Event string `msgpack:"event"`
}

// wsResponse represents a WebSocket response message.
type wsResponse struct {
	Event  string `msgpack:"event"`
	Audio  []byte `msgpack:"audio,omitempty"`
	Reason string `msgpack:"reason,omitempty"`
}

// StreamWebSocket streams text to speech over WebSocket for real-time generation.
//
// The textChan receives text chunks to synthesize. Close the channel to end streaming.
// Returns a WebSocketAudioStream that can be iterated for audio chunks.
func (s *TTSService) StreamWebSocket(ctx context.Context, textChan <-chan string, params *StreamParams, opts *WebSocketOptions) (*WebSocketAudioStream, error) {
	if opts == nil {
		opts = DefaultWebSocketOptions()
	}

	if params == nil {
		params = &StreamParams{}
	}

	// Build WebSocket URL
	wsURL := "wss://api.fish.audio/v1/tts/live"

	// Set up dialer
	dialer := websocket.Dialer{
		ReadBufferSize:  opts.ReadBufferSize,
		WriteBufferSize: opts.WriteBufferSize,
	}

	// Connect with auth and model headers
	header := http.Header{}
	header.Set("Authorization", "Bearer "+s.client.apiKey)
	if model := s.getModel(params); model != "" {
		header.Set("model", string(model))
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}

	conn.SetReadLimit(opts.MaxMessageSize)

	// Send start event with msgpack
	req := s.buildRequest(params)
	start := startEvent{
		Event:   "start",
		Request: req,
	}
	startData, err := msgpack.Marshal(start)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to marshal start event: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, startData); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to send start event: %w", err)
	}

	// Create channels for audio chunks and errors
	audioChan := make(chan []byte, 100)
	errChan := make(chan error, 1)
	doneChan := make(chan struct{})

	// Goroutine to send text chunks
	go func() {
		defer func() {
			// Send close event
			close := closeEvent{Event: "stop"}
			if data, err := msgpack.Marshal(close); err == nil {
				_ = conn.WriteMessage(websocket.BinaryMessage, data)
			}
		}()

		for {
			select {
			case text, ok := <-textChan:
				if !ok {
					return
				}
				evt := textEvent{Event: "text", Text: text}
				data, err := msgpack.Marshal(evt)
				if err != nil {
					select {
					case errChan <- fmt.Errorf("failed to marshal text event: %w", err):
					default:
					}
					return
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
					select {
					case errChan <- fmt.Errorf("failed to send text: %w", err):
					default:
					}
					return
				}
			case <-doneChan:
				return
			}
		}
	}()

	// Goroutine to receive audio chunks
	go func() {
		defer close(audioChan)
		defer func() { _ = conn.Close() }()
		defer close(doneChan)

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				// Handle normal closure and no-status-received (1005) as expected closures
				// Server often closes without a formal close frame after sending finish event
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
					return
				}
				select {
				case errChan <- err:
				default:
				}
				return
			}

			// Decode msgpack response
			var resp wsResponse
			if err := msgpack.Unmarshal(data, &resp); err != nil {
				select {
				case errChan <- fmt.Errorf("failed to decode response: %w", err):
				default:
				}
				return
			}

			switch resp.Event {
			case "audio":
				if len(resp.Audio) > 0 {
					audioChan <- resp.Audio
				}
			case "finish":
				// "stop" is normal - means we requested the stop
				// Only treat "error" as an actual error
				if resp.Reason == "error" {
					select {
					case errChan <- &WebSocketError{Message: "stream finished with error"}:
					default:
					}
				}
				return
			}
		}
	}()

	return &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}, nil
}

// WebSocketAudioStream wraps WebSocket audio chunks for iteration.
type WebSocketAudioStream struct {
	audioChan <-chan []byte
	errChan   <-chan error
	buf       []byte
	err       error
	closed    bool
	mu        sync.Mutex
}

// Next advances to the next chunk of audio data.
// Returns false when there are no more chunks or an error occurred.
func (s *WebSocketAudioStream) Next() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.err != nil {
		return false
	}

	select {
	case chunk, ok := <-s.audioChan:
		if !ok {
			s.closed = true
			return false
		}
		s.buf = chunk
		return true
	case err := <-s.errChan:
		s.err = err
		return false
	}
}

// Bytes returns the current chunk of audio data.
func (s *WebSocketAudioStream) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf
}

// Err returns any error that occurred during iteration.
func (s *WebSocketAudioStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Collect reads all audio chunks and returns them as a single byte slice.
func (s *WebSocketAudioStream) Collect() ([]byte, error) {
	var buf bytes.Buffer
	for s.Next() {
		buf.Write(s.Bytes())
	}
	if s.err != nil {
		return nil, s.err
	}
	return buf.Bytes(), nil
}

// Read implements io.Reader interface.
func (s *WebSocketAudioStream) Read(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If we have buffered data, return it
	if len(s.buf) > 0 {
		n = copy(p, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}

	// Try to get more data
	select {
	case chunk, ok := <-s.audioChan:
		if !ok {
			return 0, io.EOF
		}
		n = copy(p, chunk)
		if n < len(chunk) {
			s.buf = chunk[n:]
		}
		return n, nil
	case err := <-s.errChan:
		s.err = err
		return 0, err
	}
}

// Close closes the stream.
func (s *WebSocketAudioStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
