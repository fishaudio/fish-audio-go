package fishaudio

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// A protocol-level fake of the realtime speech-to-text endpoint, following
// asyncapi-asr-realtime.yml. The real client connects to it over a real
// WebSocket, so the tests cover the handshake, the MessagePack framing and the
// close codes, not only the SDK's own methods.

type msg = map[string]interface{}

// fakeASRServer serves /v1/asr/realtime. Each connection is handled by script.
type fakeASRServer struct {
	*httptest.Server

	mu      sync.Mutex
	headers []http.Header
	wg      sync.WaitGroup
}

// newFakeASRServer starts a server whose connections run script. The server
// is shut down, and its handlers awaited, when the test ends.
func newFakeASRServer(t *testing.T, script func(c *fakeASRConn)) *fakeASRServer {
	t.Helper()
	f := &fakeASRServer{}
	upgrader := websocket.Upgrader{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != asrRealtimePath {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		// Before the upgrade: the server's Close waits for a handler only
		// until it has taken over the connection, so counting it afterwards
		// would let a script start once the test's cleanup is over.
		f.wg.Add(1)
		defer f.wg.Done()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		script(&fakeASRConn{t: t, conn: conn})
	}))
	t.Cleanup(func() {
		f.CloseClientConnections()
		f.Close()
		f.wg.Wait()
	})
	return f
}

// newRejectingASRServer refuses the WebSocket upgrade with an HTTP status.
func newRejectingASRServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func (f *fakeASRServer) client() *Client {
	return NewClient(WithAPIKey("test-key"), WithBaseURL(f.URL))
}

// header returns the request headers of the first connection.
func (f *fakeASRServer) header() http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.headers) == 0 {
		return http.Header{}
	}
	return f.headers[0]
}

// fakeASRConn is the server side of one session.
type fakeASRConn struct {
	t    *testing.T
	conn *websocket.Conn
	seq  int

	audioBytes int
	turns      int
}

// recv reads the next client message. raw is the frame as sent; ok is false
// when the client has gone.
func (c *fakeASRConn) recv() (m msg, raw []byte, ok bool) {
	for {
		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			return nil, nil, false
		}
		if messageType != websocket.BinaryMessage {
			c.t.Errorf("server: got a non-binary frame: %q", data)
			continue
		}
		if err := msgpack.Unmarshal(data, &m); err != nil {
			c.t.Errorf("server: frame is not a MessagePack map: %v", err)
			continue
		}
		return m, data, true
	}
}

// send writes a server message, adding seq. It returns false when the client
// has gone.
func (c *fakeASRConn) send(m msg) bool {
	if _, ok := m["seq"]; !ok {
		m["seq"] = c.seq
	}
	c.seq++
	data, err := msgpack.Marshal(m)
	if err != nil {
		c.t.Errorf("server: marshal: %v", err)
		return false
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, data) == nil
}

// close sends a close frame and waits for the client to close the connection.
func (c *fakeASRConn) close(code int, reason string) {
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	c.waitClosed(2 * time.Second)
}

// waitClosed reports whether the client closed the connection within d.
func (c *fakeASRConn) waitClosed(d time.Duration) bool {
	_ = c.conn.SetReadDeadline(time.Now().Add(d))
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			netErr, isTimeout := err.(interface{ Timeout() bool })
			return !isTimeout || !netErr.Timeout()
		}
	}
}

// fail sends an error that ends the session, then closes with closeCode.
func (c *fakeASRConn) fail(errorType string, code int, message string, closeCode int) {
	c.send(msg{"type": "error", "code": code, "error_type": errorType, "message": message,
		"request_id": "req-1", "recoverable": false})
	c.close(closeCode, errorType)
}

var asrSampleRates = map[int64]bool{8000: true, 12000: true, 16000: true, 22050: true,
	24000: true, 32000: true, 44100: true, 48000: true}

func asInt(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	}
	return 0, false
}

// start reads and validates config, as the real server does, and answers with
// session_started. It returns the config, or nil if the session was refused.
func (c *fakeASRConn) start() msg {
	config, _, ok := c.recv()
	if !ok {
		return nil
	}
	if config["type"] != "config" {
		c.fail("invalid_config", 400, "the first message must be config", 4400)
		return nil
	}
	for key := range config {
		switch key {
		case "type", "protocol_version", "audio", "turn_detection", "results", "context", "client_reference_id":
		default:
			c.fail("invalid_config", 400, "unknown field "+key, 4400)
			return nil
		}
	}
	audio, _ := config["audio"].(msg)
	rate, _ := asInt(audio["sample_rate"])
	channels, _ := asInt(audio["channels"])
	if audio["format"] != "pcm_s16le" || channels != 1 || !asrSampleRates[rate] {
		c.fail("unsupported_audio", 400,
			"audio.sample_rate must be one of 8000, 12000, 16000, 22050, 24000, 32000, 44100, 48000", 4400)
		return nil
	}
	c.send(msg{
		"type": "session_started", "session_id": "s_test", "request_id": "req-1",
		"config": msg{"protocol_version": 1, "model": "transcribe-stream-1", "audio": audio,
			"results": msg{"speaker_turns": false}},
		"limits": msg{"max_session_ms": 3600000, "max_turn_ms": 60000, "max_frame_bytes": 1048576},
	})
	return config
}

// commit sends a committed turn.
func (c *fakeASRConn) commit(text, reason string) {
	c.send(msg{"type": "committed", "turn_index": c.turns, "text": text,
		"audio_start_ms": 0, "audio_end_ms": c.audioBytes / 32, "reason": reason,
		"degraded": false, "user_turn": true, "turn_kind": "user"})
	c.turns++
}

// done sends session_done and closes normally.
func (c *fakeASRConn) done() {
	c.send(msg{"type": "session_done", "stats": msg{"audio_ms": c.audioBytes / 32, "turns": c.turns, "request_id": "req-1"}})
	c.close(websocket.CloseNormalClosure, "done")
}

// serve runs a whole session the way the real server does, in a simplified
// form: the first audio opens a turn and sends a draft, finalize and
// audio_done commit it. onMessage, if set, sees every client message first.
func (c *fakeASRConn) serve(onMessage func(m msg, raw []byte)) {
	if c.start() == nil {
		return
	}
	turnOpen := false
	for {
		m, raw, ok := c.recv()
		if !ok {
			return
		}
		if onMessage != nil {
			onMessage(m, raw)
		}
		switch m["type"] {
		case "audio":
			data, isBin := m["data"].([]byte)
			if !isBin {
				c.send(msg{"type": "error", "code": 400, "error_type": "invalid_request",
					"message": "audio.data must be bin", "request_id": "req-1", "recoverable": true})
				continue
			}
			c.audioBytes += len(data)
			if !turnOpen {
				turnOpen = true
				c.send(msg{"type": "speech_start", "turn_index": c.turns, "audio_ms": 0})
				c.send(msg{"type": "draft", "turn_index": c.turns, "text": "hello", "settled_text": "",
					"audio_ms": c.audioBytes / 32})
			}
		case "finalize":
			if turnOpen {
				turnOpen = false
				c.commit("hello world", "finalize")
			}
		case "configure":
			c.send(msg{"type": "configured", "ok": true, "config": msg{}})
		case "audio_done":
			if turnOpen {
				c.commit("hello world", "audio_done")
			}
			c.done()
			return
		case "agent_speaking", "context_add", "keepalive":
		default:
			c.send(msg{"type": "error", "code": 400, "error_type": "invalid_request",
				"message": fmt.Sprintf("unknown message type %v", m["type"]), "request_id": "req-1", "recoverable": true})
		}
	}
}

// assertNoASRGoroutines fails the test if a session's goroutines are still
// running shortly after the session ended.
func assertNoASRGoroutines(t *testing.T) {
	t.Helper()
	const marker = "(*ASRRealtimeStream)."
	buf := make([]byte, 1<<20)
	deadline := time.Now().Add(asrTestWait)
	for {
		stacks := string(buf[:runtime.Stack(buf, true)])
		if !strings.Contains(stacks, marker) {
			return
		}
		if time.Now().After(deadline) {
			var leaked []string
			for _, g := range strings.Split(stacks, "\n\n") {
				if strings.Contains(g, marker) {
					leaked = append(leaked, g)
				}
			}
			t.Fatalf("%d session goroutine(s) leaked:\n%s", len(leaked), strings.Join(leaked, "\n\n"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// asrTestTimeout is far above what any of these tests takes (the slowest
// needs a few seconds under the race detector).
const asrTestTimeout = 30 * time.Second

// asrTestDeadline bounds a test that waits on sockets and goroutines: if it
// is still running after asrTestTimeout, the test binary stops with the
// test's name and every goroutine's stack, instead of sitting there until
// go test's own timeout (10 minutes by default). Call it first, so that its
// cleanup runs last and the test's other cleanups are covered too.
func asrTestDeadline(t *testing.T) {
	t.Helper()
	name := t.Name()
	timer := time.AfterFunc(asrTestTimeout, func() {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		panic(fmt.Sprintf("%s is still running after %v; goroutines:\n\n%s", name, asrTestTimeout, buf))
	})
	t.Cleanup(func() { timer.Stop() })
}

// asrTestWait is how long a test waits for something that must happen before
// it reports a hang. It is not a performance bound: it only has to be longer
// than a loaded machine needs, and shorter than asrTestTimeout.
const asrTestWait = 10 * time.Second

// within fails the test if fn does not return within asrTestWait.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	withinDuration(t, asrTestWait, what, fn)
}

func withinDuration(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		fn()
	}()
	select {
	case <-finished:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
	}
}
