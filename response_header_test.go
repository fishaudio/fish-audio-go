package fishaudio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

func TestAudioStream_HeaderBeforeAndAfterBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-fish-trace-id", "trace-123")
		_, _ = w.Write([]byte("audio"))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	stream, err := client.TTS.Stream(context.Background(), &StreamParams{Text: "Hello"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if got := stream.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("before the body is read: trace id = %q, want %q", got, "trace-123")
	}

	if _, err := stream.Collect(); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got := stream.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("after Collect closed the stream: trace id = %q, want %q", got, "trace-123")
	}
}

func TestAudioStream_HeaderWhenStreamBreaks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-fish-trace-id", "trace-123")
		w.Header().Set("Content-Length", "1000") // promises more than it sends
		_, _ = w.Write([]byte("audio"))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	stream, err := client.TTS.Stream(context.Background(), &StreamParams{Text: "Hello"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if _, err := stream.Collect(); err == nil {
		t.Fatal("Collect() error = nil, want the broken stream's error")
	}
	if got := stream.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("trace id = %q, want %q", got, "trace-123")
	}
}

func TestAudioStream_HeaderAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("audio"))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	stream, err := client.TTS.Stream(context.Background(), &StreamParams{Text: "Hello"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	if got := stream.Header.Get("x-fish-trace-id"); got != "" {
		t.Errorf("trace id = %q, want it empty", got)
	}
}

func TestAudioStream_HeaderIsPerCall(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-fish-trace-id", fmt.Sprintf("trace-%d", calls.Add(1)))
		_, _ = w.Write([]byte("audio"))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	// Both streams are open at the same time.
	first, err := client.TTS.Stream(context.Background(), &StreamParams{Text: "one"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := client.TTS.Stream(context.Background(), &StreamParams{Text: "two"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = second.Close() }()

	if got := first.Header.Get("x-fish-trace-id"); got != "trace-1" {
		t.Errorf("first stream: trace id = %q, want %q", got, "trace-1")
	}
	if got := second.Header.Get("x-fish-trace-id"); got != "trace-2" {
		t.Errorf("second stream: trace id = %q, want %q", got, "trace-2")
	}
}

func TestWebSocketAudioStream_HeaderFromHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, http.Header{"X-Fish-Trace-Id": {"trace-123"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Read the client's start and stop messages before ending the stream.
		for i := 0; i < 2; i++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
		data, _ := msgpack.Marshal(wsResponse{Event: "finish", Reason: "stop"})
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
	if got := stream.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("once open: trace id = %q, want %q", got, "trace-123")
	}

	if _, err := stream.Collect(); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	_ = stream.Close()
	if got := stream.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("after the stream ended and was closed: trace id = %q, want %q", got, "trace-123")
	}
}

func TestASRService_Transcribe_RequestIDAndHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-fish-trace-id", "trace-123")
		_, _ = w.Write([]byte(`{"text":"Hello","duration":1.5,"segments":[],"request_id":"req-123"}`))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	result, err := client.ASR.Transcribe(context.Background(), []byte("audio"), nil)
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want %q", result.RequestID, "req-123")
	}
	if got := result.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("trace id = %q, want %q", got, "trace-123")
	}
}

// The new fields do not change what an ASRResponse without a request id
// encodes to.
func TestASRResponse_JSONWithoutRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-fish-trace-id", "trace-123")
		_, _ = w.Write([]byte(`{"text":"Hello","duration":1.5,"segments":[]}`))
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	result, err := client.ASR.Transcribe(context.Background(), []byte("audio"), nil)
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.RequestID != "" {
		t.Errorf("RequestID = %q, want it empty", result.RequestID)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if want := `{"text":"Hello","duration":1.5,"segments":[]}`; string(encoded) != want {
		t.Errorf("JSON = %s, want %s", encoded, want)
	}
}

func TestWebSocketAudioStream_HeaderAfterStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, http.Header{"X-Fish-Trace-Id": {"trace-123"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for i := 0; i < 2; i++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
		data, _ := msgpack.Marshal(wsResponse{Event: "finish", Reason: "error"})
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

	// Whether Collect reports the server's error is not what this test is about.
	_, _ = stream.Collect()
	if got := stream.Header.Get("x-fish-trace-id"); got != "trace-123" {
		t.Errorf("after the stream ended with an error: trace id = %q, want %q", got, "trace-123")
	}
}
