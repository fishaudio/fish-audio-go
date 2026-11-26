package fishaudio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

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
	// Model is the TTS model to use. Options: "s2.1-pro", "s2.1-pro-free", "drama-3", "s2-pro", "s1". Default: "s2.1-pro".
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
	// Model is the TTS model to use. Options: "s2.1-pro", "s2.1-pro-free", "drama-3", "s2-pro", "s1". Default: "s2.1-pro".
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
	// Model is the TTS model to use. Options: "s2.1-pro", "s2.1-pro-free", "drama-3", "s2-pro", "s1". Default: "s2.1-pro".
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

// getModel returns the model to use, checking params then config, defaulting to s2.1-pro.
func (s *TTSService) getModel(params *StreamParams) Model {
	if params.Model != "" {
		return params.Model
	}
	if params.Config != nil && params.Config.Model != "" {
		return params.Config.Model
	}
	return ModelS21Pro
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
	Error  string `msgpack:"error,omitempty"`
}

// wsSession owns the connection of one WebSocket stream and the two goroutines
// that use it: one sends text, one reads audio.
type wsSession struct {
	conn *websocket.Conn

	// stop is closed when the stream is over, for any reason. Both goroutines
	// watch it. stopErr says why the stream ended; nil is a clean end. It is
	// written before stop is closed.
	stop     chan struct{}
	stopOnce sync.Once
	stopErr  error

	// abandoned is closed when the stream was ended by something other than
	// the server's messages: Close, a cancelled context, a failed write.
	// Audio that is still buffered is then not worth waiting for.
	abandoned chan struct{}

	connOnce sync.Once
	wg       sync.WaitGroup
}

// shutdown ends the stream. The first call wins: its cause is the stream's
// error. Safe to call from any goroutine, any number of times.
func (w *wsSession) shutdown(cause error, abandon bool) {
	w.stopOnce.Do(func() {
		w.stopErr = cause
		if abandon {
			close(w.abandoned)
		}
		close(w.stop)
		w.closeConn()
	})
}

// closeConn tells the server the client is leaving and closes the connection.
// Closing the connection also unblocks a goroutine that is reading or writing.
func (w *wsSession) closeConn() {
	w.connOnce.Do(func() {
		msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = w.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
		_ = w.conn.Close()
	})
}

// send writes one event. A failed write ends the stream with that error.
func (w *wsSession) send(event any, what string) bool {
	data, err := msgpack.Marshal(event)
	if err != nil {
		w.shutdown(fmt.Errorf("failed to marshal %s: %w", what, err), true)
		return false
	}
	if err := w.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		w.shutdown(fmt.Errorf("failed to send %s: %w", what, err), true)
		return false
	}
	return true
}

// sendText forwards text chunks until textChan is closed or the stream is over.
func (w *wsSession) sendText(textChan <-chan string) {
	for {
		select {
		case text, ok := <-textChan:
			if !ok {
				w.send(closeEvent{Event: "stop"}, "stop event")
				return
			}
			if !w.send(textEvent{Event: "text", Text: text}, "text") {
				return
			}
		case <-w.stop:
			return
		}
	}
}

// readAudio reads server messages until the stream is over and returns why it
// ended; nil is a clean end.
func (w *wsSession) readAudio(audioChan chan<- []byte) error {
	for {
		_, data, err := w.conn.ReadMessage()
		if err != nil {
			// The server ends a stream with a finish message. A connection
			// that goes away before that, whatever the close code, has not
			// delivered the whole stream.
			return &WebSocketError{
				Message: fmt.Sprintf("websocket closed before the stream finished: %v", err),
				Err:     err,
			}
		}

		var resp wsResponse
		if err := msgpack.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		switch resp.Event {
		case "audio":
			if len(resp.Audio) > 0 {
				select {
				case audioChan <- resp.Audio:
				case <-w.stop:
					return nil
				}
			}
		case "error":
			// The server rejected the request, for example an unknown
			// reference_id. It sends nothing more and keeps the connection
			// open, so this message is the end of the stream.
			msg := resp.Error
			if msg == "" {
				msg = "server reported an error"
			}
			return &WebSocketError{Message: msg}
		case "finish":
			// "stop" is normal - means we requested the stop
			// Only treat "error" as an actual error
			if resp.Reason == "error" {
				return &WebSocketError{Message: "stream finished with error"}
			}
			return nil
		}
	}
}

// StreamWebSocket streams text to speech over WebSocket for real-time generation.
//
// The textChan receives text chunks to synthesize. Close the channel to end streaming.
// Returns a WebSocketAudioStream that can be iterated for audio chunks.
//
// The stream ends when the server finishes, when the server reports an error,
// when the connection is lost, when ctx is cancelled, or when the caller calls
// Close. Every end releases the connection. After the stream has ended, text
// is no longer read from textChan.
func (s *TTSService) StreamWebSocket(ctx context.Context, textChan <-chan string, params *StreamParams, opts *WebSocketOptions) (*WebSocketAudioStream, error) {
	if opts == nil {
		opts = DefaultWebSocketOptions()
	}

	if params == nil {
		params = &StreamParams{}
	}

	// Build WebSocket URL from baseURL
	wsURL := strings.Replace(strings.Replace(s.client.baseURL, "https://", "wss://", 1), "http://", "ws://", 1) + "/v1/tts/live"

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

	audioChan := make(chan []byte, 100)
	errChan := make(chan error, 1)
	sess := &wsSession{
		conn:      conn,
		stop:      make(chan struct{}),
		abandoned: make(chan struct{}),
	}

	// Cancelling ctx ends the stream.
	stopWatchingCtx := context.AfterFunc(ctx, func() {
		sess.shutdown(ctx.Err(), true)
	})

	sess.wg.Add(2)

	// Goroutine to send text chunks
	go func() {
		defer sess.wg.Done()
		sess.sendText(textChan)
	}()

	// Goroutine to receive audio chunks
	go func() {
		defer sess.wg.Done()
		// The stream's error is in errChan before audioChan is closed, so a
		// caller that has read all audio always finds it.
		defer close(audioChan)

		sess.shutdown(sess.readAudio(audioChan), false)
		stopWatchingCtx()
		if sess.stopErr != nil {
			errChan <- sess.stopErr
		}
	}()

	return &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
		sess:      sess,
	}, nil
}

// WebSocketAudioStream wraps WebSocket audio chunks for iteration.
//
// Call Close when you stop reading before the stream has ended; it releases
// the connection. A stream that was read to its end has already released it.
type WebSocketAudioStream struct {
	audioChan <-chan []byte
	errChan   <-chan error
	buf       []byte
	err       error
	closed    bool
	mu        sync.Mutex

	// sess is nil on a stream that has no connection behind it.
	sess *wsSession
}

// recv waits for the next chunk of audio. ok is false when the stream is over;
// s.err then says why. The caller holds s.mu.
func (s *WebSocketAudioStream) recv() (chunk []byte, ok bool) {
	if s.closed || s.err != nil {
		return nil, false
	}

	var abandoned <-chan struct{}
	if s.sess != nil {
		abandoned = s.sess.abandoned
	}

	select {
	case chunk, ok := <-s.audioChan:
		if ok {
			return chunk, true
		}
		// All audio has been read. The error, if any, is already waiting.
		select {
		case s.err = <-s.errChan:
		default:
		}
	case <-abandoned:
		s.err = s.sess.stopErr
	}
	s.closed = true
	return nil, false
}

// Next advances to the next chunk of audio data.
// Returns false when there are no more chunks or an error occurred.
// Audio that arrived before an error is returned first.
func (s *WebSocketAudioStream) Next() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	chunk, ok := s.recv()
	if !ok {
		return false
	}
	s.buf = chunk
	return true
}

// Bytes returns the current chunk of audio data.
func (s *WebSocketAudioStream) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf
}

// Err returns any error that occurred during iteration.
// It is nil after a stream that finished normally and after Close.
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
	if err := s.Err(); err != nil {
		return nil, err
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
	chunk, ok := s.recv()
	if !ok {
		if s.err != nil {
			return 0, s.err
		}
		return 0, io.EOF
	}
	n = copy(p, chunk)
	if n < len(chunk) {
		s.buf = chunk[n:]
	}
	return n, nil
}

// Close ends the stream and releases its connection. When it returns, the
// connection is closed and the stream's goroutines have exited; Next returns
// false. Close unblocks a Next or Read that is waiting in another goroutine.
// It is safe to call more than once and after the stream has ended.
func (s *WebSocketAudioStream) Close() error {
	if s.sess != nil {
		s.sess.shutdown(nil, true)
		s.sess.wg.Wait()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
