package fishaudio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// --- How a StreamWebSocket stream ends ---

// wsEndTimeout is how long a test waits for something that should happen at once.
const wsEndTimeout = 2 * time.Second

// streamGoroutines counts the live goroutines that StreamWebSocket started.
func streamGoroutines() int {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	n := 0
	for _, g := range strings.Split(buf.String(), "\n\n") {
		if strings.Contains(g, "created by github.com/fishaudio/fish-audio-go.(*TTSService).StreamWebSocket") {
			n++
		}
	}
	return n
}

// requireNoStreamGoroutines fails the test if goroutines of a stream are still
// alive after wait. Pass 0 where they must be gone already.
func requireNoStreamGoroutines(t *testing.T, wait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		n := streamGoroutines()
		if n == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			t.Errorf("%d stream goroutines still alive, want 0", n)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// wsEndServer is a local server for /v1/tts/live. It reads the start event,
// runs serve in its own goroutine, and reports on clientGone how the client's
// side of the connection ended. Like the real API, it never closes the
// connection itself unless serve does.
func wsEndServer(t *testing.T, serve func(conn *websocket.Conn)) (client *Client, clientGone <-chan error) {
	t.Helper()
	gone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read start event
		if _, _, err := conn.ReadMessage(); err != nil {
			gone <- err
			return
		}
		go serve(conn)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				gone <- err
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL)), gone
}

func wsSend(conn *websocket.Conn, resp wsResponse) error {
	data, _ := msgpack.Marshal(resp)
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

// requireClientGone fails the test if the server does not see the client
// leave. Without it the connection would stay open on both sides.
func requireClientGone(t *testing.T, clientGone <-chan error) {
	t.Helper()
	select {
	case <-clientGone:
	case <-time.After(wsEndTimeout):
		t.Error("server did not see the client close the connection")
	}
}

// collectWithin runs Collect and fails the test if the stream does not end.
func collectWithin(t *testing.T, stream *WebSocketAudioStream) ([]byte, error) {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := stream.Collect()
		done <- result{data, err}
	}()
	select {
	case r := <-done:
		return r.data, r.err
	case <-time.After(wsEndTimeout):
		t.Fatalf("stream did not end within %v", wsEndTimeout)
		return nil, nil
	}
}

func closedTextChan() chan string {
	textChan := make(chan string)
	close(textChan)
	return textChan
}

func TestTTSService_StreamWebSocket_CloseReleasesConnection(t *testing.T) {
	// The server keeps sending audio and never finishes.
	client, clientGone := wsEndServer(t, func(conn *websocket.Conn) {
		for wsSend(conn, wsResponse{Event: "audio", Audio: []byte("audio_chunk")}) == nil {
			time.Sleep(time.Millisecond)
		}
	})

	// The caller never closes the text channel.
	stream, err := client.TTS.StreamWebSocket(context.Background(), make(chan string), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	if !stream.Next() {
		t.Fatalf("Next() = false, want a chunk; Err() = %v", stream.Err())
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	requireNoStreamGoroutines(t, 0)
	requireClientGone(t, clientGone)

	if stream.Next() {
		t.Error("Next() should return false after Close()")
	}
	if err := stream.Err(); err != nil {
		t.Errorf("Err() = %v, want nil after Close()", err)
	}
	if err := stream.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestTTSService_StreamWebSocket_CloseWithUnreadAudio(t *testing.T) {
	// More audio than the stream buffers, then finish. The caller reads none of it.
	sent := make(chan struct{})
	client, clientGone := wsEndServer(t, func(conn *websocket.Conn) {
		for i := 0; i < 300; i++ {
			_ = wsSend(conn, wsResponse{Event: "audio", Audio: []byte("audio_chunk")})
		}
		_ = wsSend(conn, wsResponse{Event: "finish", Reason: "stop"})
		close(sent)
	})

	stream, err := client.TTS.StreamWebSocket(context.Background(), closedTextChan(), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	<-sent
	time.Sleep(100 * time.Millisecond) // let the reader fill the buffer

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	requireNoStreamGoroutines(t, 0)
	requireClientGone(t, clientGone)
}

func TestTTSService_StreamWebSocket_CloseUnblocksNext(t *testing.T) {
	// The server sends nothing.
	client, clientGone := wsEndServer(t, func(conn *websocket.Conn) {})

	stream, err := client.TTS.StreamWebSocket(context.Background(), make(chan string), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	nextDone := make(chan bool, 1)
	go func() { nextDone <- stream.Next() }()
	time.Sleep(50 * time.Millisecond) // Next is now waiting for audio

	closeDone := make(chan error, 1)
	go func() { closeDone <- stream.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close() error = %v", err)
		}
	case <-time.After(wsEndTimeout):
		t.Fatal("Close() did not return while Next() was waiting")
	}
	select {
	case ok := <-nextDone:
		if ok {
			t.Error("Next() = true, want false after Close()")
		}
	case <-time.After(wsEndTimeout):
		t.Fatal("Next() did not return after Close()")
	}
	if err := stream.Err(); err != nil {
		t.Errorf("Err() = %v, want nil after Close()", err)
	}
	if n, err := stream.Read(make([]byte, 16)); n != 0 || err != io.EOF {
		t.Errorf("Read() = %d, %v, want 0, io.EOF after Close()", n, err)
	}
	requireNoStreamGoroutines(t, 0)
	requireClientGone(t, clientGone)
}

func TestTTSService_StreamWebSocket_CloseAfterFinish(t *testing.T) {
	client, clientGone := wsEndServer(t, func(conn *websocket.Conn) {
		_ = wsSend(conn, wsResponse{Event: "audio", Audio: []byte("audio_chunk")})
		_ = wsSend(conn, wsResponse{Event: "finish", Reason: "stop"})
	})

	stream, err := client.TTS.StreamWebSocket(context.Background(), closedTextChan(), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	data, err := collectWithin(t, stream)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if string(data) != "audio_chunk" {
		t.Errorf("Collect() = %q, want %q", string(data), "audio_chunk")
	}

	// A finished stream has released everything without Close.
	requireNoStreamGoroutines(t, wsEndTimeout)
	requireClientGone(t, clientGone)

	for i := 0; i < 2; i++ {
		if err := stream.Close(); err != nil {
			t.Errorf("Close() after finish error = %v", err)
		}
	}
	if err := stream.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestTTSService_StreamWebSocket_ServerErrorMessage(t *testing.T) {
	// What the API does for an unknown reference_id: one error message, then
	// nothing, with the connection left open.
	client, clientGone := wsEndServer(t, func(conn *websocket.Conn) {
		data, _ := msgpack.Marshal(map[string]string{"event": "error", "error": "Reference not found"})
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	})

	stream, err := client.TTS.StreamWebSocket(context.Background(), closedTextChan(), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	_, err = collectWithin(t, stream)
	var wsErr *WebSocketError
	if !errors.As(err, &wsErr) {
		t.Fatalf("expected *WebSocketError, got %T: %v", err, err)
	}
	if wsErr.Message != "Reference not found" {
		t.Errorf("error message = %q, want %q", wsErr.Message, "Reference not found")
	}
	if stream.Next() {
		t.Error("Next() should return false after error")
	}

	requireNoStreamGoroutines(t, wsEndTimeout)
	requireClientGone(t, clientGone)
}

func TestTTSService_StreamWebSocket_ClosedBeforeFinish(t *testing.T) {
	tests := []struct {
		name string
		// end closes the connection from the server side.
		end      func(conn *websocket.Conn)
		wantCode int // 0: no close frame arrives
	}{
		{
			// What the API does for a start event it cannot read, for example
			// an unknown format.
			name: "close frame without status code",
			end: func(conn *websocket.Conn) {
				_ = conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(time.Second))
			},
			wantCode: websocket.CloseNoStatusReceived,
		},
		{
			name: "normal closure",
			end: func(conn *websocket.Conn) {
				msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
				_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
			},
			wantCode: websocket.CloseNormalClosure,
		},
		{
			name: "connection dropped",
			end: func(conn *websocket.Conn) {
				_ = conn.UnderlyingConn().Close()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := wsEndServer(t, func(conn *websocket.Conn) {
				_ = wsSend(conn, wsResponse{Event: "audio", Audio: []byte("audio_chunk")})
				tt.end(conn)
			})

			stream, err := client.TTS.StreamWebSocket(context.Background(), closedTextChan(), nil, nil)
			if err != nil {
				t.Fatalf("StreamWebSocket() error = %v", err)
			}
			time.Sleep(50 * time.Millisecond) // the connection is gone before the caller reads

			// Audio that arrived before the connection closed is not lost.
			if !stream.Next() {
				t.Fatalf("Next() = false, want the chunk sent before the close; Err() = %v", stream.Err())
			}
			if string(stream.Bytes()) != "audio_chunk" {
				t.Errorf("Bytes() = %q, want %q", string(stream.Bytes()), "audio_chunk")
			}

			_, err = collectWithin(t, stream)
			var wsErr *WebSocketError
			if !errors.As(err, &wsErr) {
				t.Fatalf("expected *WebSocketError, got %T: %v", err, err)
			}
			if !strings.HasPrefix(wsErr.Message, "websocket closed before the stream finished") {
				t.Errorf("error message = %q, want it to say the stream did not finish", wsErr.Message)
			}
			if tt.wantCode != 0 {
				var closeErr *websocket.CloseError
				if !errors.As(err, &closeErr) {
					t.Fatalf("expected the error to wrap *websocket.CloseError, got %v", err)
				}
				if closeErr.Code != tt.wantCode {
					t.Errorf("close code = %d, want %d", closeErr.Code, tt.wantCode)
				}
			}

			requireNoStreamGoroutines(t, wsEndTimeout)
		})
	}
}

func TestTTSService_StreamWebSocket_FinishErrorAlwaysReported(t *testing.T) {
	client, _ := wsEndServer(t, func(conn *websocket.Conn) {
		_ = wsSend(conn, wsResponse{Event: "finish", Reason: "error"})
	})

	// The error must reach the caller every time, also when the stream has
	// already ended before the caller starts to read.
	for i := 0; i < 30; i++ {
		stream, err := client.TTS.StreamWebSocket(context.Background(), closedTextChan(), nil, nil)
		if err != nil {
			t.Fatalf("StreamWebSocket() error = %v", err)
		}
		time.Sleep(10 * time.Millisecond)

		_, err = collectWithin(t, stream)
		var wsErr *WebSocketError
		if !errors.As(err, &wsErr) {
			t.Fatalf("stream %d: expected *WebSocketError, got %T: %v", i, err, err)
		}
		if wsErr.Message != "stream finished with error" {
			t.Errorf("error message = %q, want %q", wsErr.Message, "stream finished with error")
		}
	}

	requireNoStreamGoroutines(t, wsEndTimeout)
}

func TestTTSService_StreamWebSocket_ContextCancel(t *testing.T) {
	// The server sends nothing.
	client, clientGone := wsEndServer(t, func(conn *websocket.Conn) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.TTS.StreamWebSocket(ctx, make(chan string), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	time.AfterFunc(50*time.Millisecond, cancel)

	_, err = collectWithin(t, stream)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Collect() error = %v, want %v", err, context.Canceled)
	}

	requireNoStreamGoroutines(t, wsEndTimeout)
	requireClientGone(t, clientGone)

	if err := stream.Close(); err != nil {
		t.Errorf("Close() after cancel error = %v", err)
	}
	if !errors.Is(stream.Err(), context.Canceled) {
		t.Errorf("Err() = %v, want %v", stream.Err(), context.Canceled)
	}
}
