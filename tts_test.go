package fishaudio

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
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

func TestTTSService_StreamIteratesThroughFinalChunk(t *testing.T) {
	audioData := []byte("complete audio response")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audioData)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	stream, err := client.TTS.Stream(context.Background(), &StreamParams{Text: "Hello"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	var received bytes.Buffer
	for stream.Next() {
		received.Write(stream.Bytes())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if !bytes.Equal(received.Bytes(), audioData) {
		t.Errorf("received = %q, want %q", received.Bytes(), audioData)
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

// --- WebSocketAudioStream unit tests ---

func TestWebSocketAudioStream_NextAndBytes(t *testing.T) {
	audioChan := make(chan []byte, 3)
	errChan := make(chan error, 1)

	audioChan <- []byte("chunk1")
	audioChan <- []byte("chunk2")
	audioChan <- []byte("chunk3")
	close(audioChan)

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	var collected bytes.Buffer
	count := 0
	for stream.Next() {
		collected.Write(stream.Bytes())
		count++
	}

	if count != 3 {
		t.Errorf("chunk count = %d, want %d", count, 3)
	}
	if collected.String() != "chunk1chunk2chunk3" {
		t.Errorf("collected = %q, want %q", collected.String(), "chunk1chunk2chunk3")
	}
	if stream.Err() != nil {
		t.Errorf("Err() = %v, want nil", stream.Err())
	}
}

func TestWebSocketAudioStream_Error(t *testing.T) {
	audioChan := make(chan []byte, 1)
	errChan := make(chan error, 1)
	errChan <- io.ErrUnexpectedEOF

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	if stream.Next() {
		t.Error("Next() should return false when error is available")
	}
	if stream.Err() != io.ErrUnexpectedEOF {
		t.Errorf("Err() = %v, want %v", stream.Err(), io.ErrUnexpectedEOF)
	}
	// Subsequent Next() should also return false
	if stream.Next() {
		t.Error("Next() should return false after error")
	}
}

func TestWebSocketAudioStream_Collect(t *testing.T) {
	audioChan := make(chan []byte, 3)
	errChan := make(chan error, 1)

	audioChan <- []byte("aaa")
	audioChan <- []byte("bbb")
	audioChan <- []byte("ccc")
	close(audioChan)

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	data, err := stream.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if string(data) != "aaabbbccc" {
		t.Errorf("Collect() = %q, want %q", string(data), "aaabbbccc")
	}
}

func TestWebSocketAudioStream_CollectError(t *testing.T) {
	audioChan := make(chan []byte, 2)
	errChan := make(chan error, 1)

	audioChan <- []byte("data")
	errChan <- io.ErrUnexpectedEOF

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	_, err := stream.Collect()
	if err == nil {
		t.Fatal("Collect() expected error, got nil")
	}
}

func TestWebSocketAudioStream_Read(t *testing.T) {
	audioChan := make(chan []byte, 2)
	errChan := make(chan error, 1)

	audioChan <- []byte("hello world")
	close(audioChan)

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	// Read with small buffer to test partial reads
	buf := make([]byte, 5)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 5 {
		t.Errorf("Read() n = %d, want %d", n, 5)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("Read() = %q, want %q", string(buf[:n]), "hello")
	}

	// Read remaining buffered data
	n, err = stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(buf[:n]) != " worl" {
		t.Errorf("Read() = %q, want %q", string(buf[:n]), " worl")
	}

	// Read last byte
	n, err = stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(buf[:n]) != "d" {
		t.Errorf("Read() = %q, want %q", string(buf[:n]), "d")
	}
}

func TestWebSocketAudioStream_ReadEOF(t *testing.T) {
	audioChan := make(chan []byte)
	errChan := make(chan error, 1)
	close(audioChan)

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	buf := make([]byte, 10)
	n, err := stream.Read(buf)
	if n != 0 {
		t.Errorf("Read() n = %d, want 0", n)
	}
	if err != io.EOF {
		t.Errorf("Read() err = %v, want io.EOF", err)
	}
}

func TestWebSocketAudioStream_ReadError(t *testing.T) {
	audioChan := make(chan []byte, 1)
	errChan := make(chan error, 1)
	errChan <- io.ErrUnexpectedEOF

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	buf := make([]byte, 10)
	n, err := stream.Read(buf)
	if n != 0 {
		t.Errorf("Read() n = %d, want 0", n)
	}
	if err != io.ErrUnexpectedEOF {
		t.Errorf("Read() err = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestWebSocketAudioStream_Close(t *testing.T) {
	audioChan := make(chan []byte, 1)
	errChan := make(chan error, 1)

	audioChan <- []byte("data")

	stream := &WebSocketAudioStream{
		audioChan: audioChan,
		errChan:   errChan,
	}

	err := stream.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// After close, Next() should return false
	if stream.Next() {
		t.Error("Next() should return false after Close()")
	}
}

// --- StreamWebSocket integration tests ---

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func TestTTSService_StreamWebSocket_BasicFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-key")
		}

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Upgrade error: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		// Read start event
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage error: %v", err)
			return
		}

		var start startEvent
		if err := msgpack.Unmarshal(data, &start); err != nil {
			t.Fatalf("unmarshal start: %v", err)
			return
		}
		if start.Event != "start" {
			t.Errorf("start event = %q, want %q", start.Event, "start")
		}
		if start.Request == nil {
			t.Fatal("start request is nil")
		}

		// Read text events until stop event
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				break
			}

			var msg map[string]interface{}
			if err := msgpack.Unmarshal(data, &msg); err != nil {
				break
			}

			event, _ := msg["event"].(string)
			if event == "stop" {
				break
			}

			// Send audio response for each text
			audioResp := wsResponse{Event: "audio", Audio: []byte("audio_chunk")}
			respData, _ := msgpack.Marshal(audioResp)
			_ = conn.WriteMessage(websocket.BinaryMessage, respData)
		}

		// Send finish event
		finishResp := wsResponse{Event: "finish", Reason: "stop"}
		finishData, _ := msgpack.Marshal(finishResp)
		_ = conn.WriteMessage(websocket.BinaryMessage, finishData)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	textChan := make(chan string, 2)
	textChan <- "Hello"
	textChan <- "World"
	close(textChan)

	stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, &StreamParams{
		Text: "test",
	}, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	data, err := stream.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if len(data) == 0 {
		t.Error("expected audio data, got empty")
	}
}

func TestTTSService_StreamWebSocket_ErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read start event
		_, _, _ = conn.ReadMessage()

		// Send error finish event
		resp := wsResponse{Event: "finish", Reason: "error"}
		data, _ := msgpack.Marshal(resp)
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	textChan := make(chan string)
	close(textChan)

	stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	_, err = stream.Collect()
	if err == nil {
		t.Fatal("expected WebSocketError, got nil")
	}

	wsErr, ok := err.(*WebSocketError)
	if !ok {
		t.Fatalf("expected *WebSocketError, got %T: %v", err, err)
	}
	if wsErr.Message != "stream finished with error" {
		t.Errorf("error message = %q, want %q", wsErr.Message, "stream finished with error")
	}
}

func TestTTSService_StreamWebSocket_ConnectionError(t *testing.T) {
	client := NewClient(WithAPIKey("test-key"), WithBaseURL("http://127.0.0.1:1"))

	textChan := make(chan string)
	close(textChan)

	_, err := client.TTS.StreamWebSocket(context.Background(), textChan, nil, nil)
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}

	expected := "websocket dial failed"
	if !bytes.Contains([]byte(err.Error()), []byte(expected)) {
		t.Errorf("error = %q, want to contain %q", err.Error(), expected)
	}
}

func TestTTSService_StreamWebSocket_NilOpts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read start event
		_, _, _ = conn.ReadMessage()

		// Send finish
		resp := wsResponse{Event: "finish", Reason: "stop"}
		data, _ := msgpack.Marshal(resp)
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	textChan := make(chan string)
	close(textChan)

	// nil opts should use defaults without panic
	stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	_, err = stream.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
}

func TestTTSService_StreamWebSocket_WithModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify model header is set in upgrade request
		model := r.Header.Get("model")
		if model != "speech-1.6" {
			t.Errorf("model header = %q, want %q", model, "speech-1.6")
		}

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read start, send finish
		_, _, _ = conn.ReadMessage()

		resp := wsResponse{Event: "finish", Reason: "stop"}
		data, _ := msgpack.Marshal(resp)
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	textChan := make(chan string)
	close(textChan)

	stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, &StreamParams{
		Model: ModelSpeech16,
	}, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	// Give time for goroutines to complete
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	done := make(chan struct{})
	go func() {
		_, _ = stream.Collect()
		close(done)
	}()

	select {
	case <-done:
	case <-timer.C:
		t.Fatal("test timed out")
	}
}

func TestTTSService_Stream_DefaultModel(t *testing.T) {
	tests := []struct {
		name   string
		params *StreamParams
		want   string
	}{
		{"default", &StreamParams{Text: "Hello"}, "s2.1-pro"},
		{"params model", &StreamParams{Text: "Hello", Model: ModelDrama3}, "drama-3"},
		{"config model", &StreamParams{Text: "Hello", Config: &TTSConfig{Model: ModelS21ProFree}}, "s2.1-pro-free"},
		{"params over config", &StreamParams{Text: "Hello", Model: ModelS2Pro, Config: &TTSConfig{Model: ModelDrama3}}, "s2-pro"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("model")
				_, _ = w.Write([]byte("audio"))
			}))
			defer server.Close()

			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
			stream, err := client.TTS.Stream(context.Background(), tt.params)
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if _, err := stream.Collect(); err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("model header = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTTSService_StreamWebSocket_DefaultModel(t *testing.T) {
	modelCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCh <- r.Header.Get("model")

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read start, send finish
		_, _, _ = conn.ReadMessage()

		resp := wsResponse{Event: "finish", Reason: "stop"}
		data, _ := msgpack.Marshal(resp)
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	textChan := make(chan string)
	close(textChan)

	stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, &StreamParams{}, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	select {
	case got := <-modelCh:
		if got != "s2.1-pro" {
			t.Errorf("model header = %q, want %q", got, "s2.1-pro")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for WebSocket upgrade request")
	}
}

// --- WebSocketOptions.Header ---

// newHandshakeServer starts a WebSocket server that sends the headers of each
// handshake it receives to the returned channel, then finishes the stream.
func newHandshakeServer(t *testing.T) (*Client, <-chan http.Header) {
	t.Helper()
	got := make(chan http.Header, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _, _ = conn.ReadMessage()
		data, _ := msgpack.Marshal(wsResponse{Event: "finish", Reason: "stop"})
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	}))
	t.Cleanup(server.Close)
	return NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL)), got
}

// streamWebSocket opens a stream with opts, collects it and returns the
// headers the server received on the handshake.
func streamWebSocket(t *testing.T, client *Client, got <-chan http.Header, opts *WebSocketOptions) http.Header {
	t.Helper()
	textChan := make(chan string)
	close(textChan)
	stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, &StreamParams{}, opts)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	if _, err := stream.Collect(); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return <-got
}

func TestTTSService_StreamWebSocket_Header(t *testing.T) {
	client, got := newHandshakeServer(t)
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	opts := DefaultWebSocketOptions()
	opts.Header = http.Header{
		"traceparent": {tp}, // non-canonical key, as a map literal allows
		"X-Multi":     {"a", "b"},
	}
	h := streamWebSocket(t, client, got, opts)

	if v := h.Values("Traceparent"); len(v) != 1 || v[0] != tp {
		t.Errorf("traceparent = %q, want [%q]", v, tp)
	}
	if v := h.Values("X-Multi"); len(v) != 2 || v[0] != "a" || v[1] != "b" {
		t.Errorf("X-Multi = %q, want [a b]", v)
	}
}

func TestTTSService_StreamWebSocket_HeaderCannotReplaceAuthOrModel(t *testing.T) {
	client, got := newHandshakeServer(t)

	opts := DefaultWebSocketOptions()
	opts.Header = http.Header{
		"Authorization": {"Bearer other"},
		"authorization": {"Bearer other"},
		"Model":         {"other"},
		"MODEL":         {"other"},
	}
	before := opts.Header.Clone()
	h := streamWebSocket(t, client, got, opts)

	if v := h.Values("Authorization"); len(v) != 1 || v[0] != "Bearer test-key" {
		t.Errorf("Authorization = %q, want [Bearer test-key]", v)
	}
	if v := h.Values("Model"); len(v) != 1 || v[0] != "s2.1-pro" {
		t.Errorf("model = %q, want [s2.1-pro]", v)
	}
	if !reflect.DeepEqual(opts.Header, before) {
		t.Errorf("caller's Header changed: %v, want %v", opts.Header, before)
	}
}

func TestTTSService_StreamWebSocket_NilAndDefaultOptionsHeaders(t *testing.T) {
	for name, opts := range map[string]*WebSocketOptions{
		"nil":     nil,
		"default": DefaultWebSocketOptions(),
	} {
		t.Run(name, func(t *testing.T) {
			client, got := newHandshakeServer(t)
			h := streamWebSocket(t, client, got, opts)

			if v := h.Values("Authorization"); len(v) != 1 || v[0] != "Bearer test-key" {
				t.Errorf("Authorization = %q, want [Bearer test-key]", v)
			}
			if v := h.Values("Model"); len(v) != 1 || v[0] != "s2.1-pro" {
				t.Errorf("model = %q, want [s2.1-pro]", v)
			}
			for k := range h {
				switch k {
				case "Authorization", "Model", "Connection", "Upgrade", "User-Agent",
					"Sec-Websocket-Key", "Sec-Websocket-Version":
				default:
					t.Errorf("unexpected handshake header %q", k)
				}
			}
		})
	}
}

func TestTTSService_StreamWebSocket_ConcurrentHeaders(t *testing.T) {
	client, got := newHandshakeServer(t)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			opts := DefaultWebSocketOptions()
			opts.Header = http.Header{"X-Stream": {strconv.Itoa(i)}}
			textChan := make(chan string)
			close(textChan)
			stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, nil, opts)
			if err == nil {
				_, err = stream.Collect()
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("StreamWebSocket() error = %v", err)
		}
	}

	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		v := (<-got).Values("X-Stream")
		if len(v) != 1 {
			t.Fatalf("X-Stream = %q, want one value", v)
		}
		if seen[v[0]] {
			t.Errorf("X-Stream %q sent twice", v[0])
		}
		seen[v[0]] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct X-Stream values, want %d", len(seen), n)
	}
}
