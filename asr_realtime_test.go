package fishaudio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// pcm returns ms milliseconds of 16 kHz 16-bit mono audio.
func pcm(ms int) []byte {
	return bytes.Repeat([]byte{0x01, 0x02}, 16*ms)
}

// audioOf returns a closed channel holding chunks: a finished audio source.
func audioOf(chunks ...[]byte) <-chan []byte {
	ch := make(chan []byte, len(chunks))
	for _, chunk := range chunks {
		ch <- chunk
	}
	close(ch)
	return ch
}

// collect reads a stream to its end.
func collect(t *testing.T, stream *ASRRealtimeStream) []ASREvent {
	t.Helper()
	var events []ASREvent
	within(t, "reading the stream", func() {
		for stream.Next() {
			events = append(events, stream.Event())
		}
	})
	return events
}

func eventTypes(events []ASREvent) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.EventType()
	}
	return types
}

// shortSettle shortens the wait for a ctx past its deadline to be done, for
// tests with a ctx that never is.
func shortSettle(t *testing.T) {
	t.Helper()
	old := asrCtxSettle
	asrCtxSettle = 20 * time.Millisecond
	t.Cleanup(func() { asrCtxSettle = old })
}

// shortGrace shortens the wait for a close frame, for tests where none comes.
func shortGrace(t *testing.T) {
	t.Helper()
	old := asrCloseGrace
	asrCloseGrace = 100 * time.Millisecond
	t.Cleanup(func() { asrCloseGrace = old })
}

func mustStream(t *testing.T, f *fakeASRServer, ctx context.Context, audio <-chan []byte, params *ASRRealtimeParams) *ASRRealtimeStream {
	t.Helper()
	stream, err := f.client().ASR.StreamWebSocket(ctx, audio, params, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	t.Cleanup(func() {
		_ = stream.Close()
		assertNoASRGoroutines(t)
	})
	return stream
}

// Normal flow and wire format.

func TestASRRealtime_BasicFlow(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) { c.serve(nil) })

	stream := mustStream(t, f, context.Background(), audioOf(pcm(40), pcm(40), pcm(40)), nil)
	events := collect(t, stream)
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}

	want := []string{"session_started", "speech_start", "draft", "committed", "session_done"}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}

	started := events[0].(*ASRSessionStartedEvent)
	if started.SessionID != "s_test" || started.RequestID != "req-1" {
		t.Errorf("session_started = %+v", started)
	}
	if started.Limits.MaxFrameBytes != 1048576 || started.Limits.MaxTurnMS != 60000 {
		t.Errorf("limits = %+v", started.Limits)
	}
	// A field the spec does not list (the real server sends this one) is kept.
	if results, _ := started.Config["results"].(map[string]interface{}); results["speaker_turns"] != false {
		t.Errorf("config = %v", started.Config)
	}
	committed := events[3].(*ASRCommittedEvent)
	if committed.Text != "hello world" || committed.Reason != "audio_done" || !committed.UserTurn || committed.AudioEndMS != 120 {
		t.Errorf("committed = %+v", committed)
	}
	done := events[4].(*ASRSessionDoneEvent)
	if done.Stats.AudioMS != 120 || done.Stats.Turns != 1 || done.Seq != 4 {
		t.Errorf("session_done = %+v", done)
	}

	header := f.header()
	if got := header.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
	if got := header.Get("model"); got != "transcribe-stream-1" {
		t.Errorf("model header = %q, want the realtime default", got)
	}
	if got := header.Values("User-Agent"); len(got) != 1 || got[0] != userAgent {
		t.Errorf("handshake User-Agent = %q, want exactly [%q]", got, userAgent)
	}
}

// A refused handshake carried the User-Agent too, like every other request.
func TestASRRealtime_RefusedHandshakeSentUserAgent(t *testing.T) {
	asrTestDeadline(t)
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, err := client.ASR.StreamWebSocket(context.Background(), make(chan []byte), nil, nil)
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("error = %T (%v), want *AuthenticationError", err, err)
	}
	if got := (<-headers).Values("User-Agent"); len(got) != 1 || got[0] != userAgent {
		t.Errorf("handshake User-Agent = %q, want exactly [%q]", got, userAgent)
	}
	assertNoASRGoroutines(t)
}

func TestASRRealtime_ModelHeader(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) { c.serve(nil) })
	stream := mustStream(t, f, context.Background(), audioOf(), &ASRRealtimeParams{Model: "transcribe-1-pro-realtime"})
	collect(t, stream)
	if got := f.header().Get("model"); got != "transcribe-1-pro-realtime" {
		t.Errorf("model header = %q", got)
	}
}

func TestASRRealtime_ConfigMessage(t *testing.T) {
	asrTestDeadline(t)
	tests := []struct {
		name   string
		params *ASRRealtimeParams
		want   msg
	}{
		{
			name:   "defaults send only the audio format",
			params: nil,
			want: msg{"type": "config",
				"audio": msg{"format": "pcm_s16le", "sample_rate": int64(16000), "channels": int64(1)}},
		},
		{
			name: "diarize false is sent, unset options are not",
			params: &ASRRealtimeParams{
				SampleRate: 48000,
				Results:    &ASRResults{Diarize: Bool(false), Drafts: Bool(true)},
			},
			want: msg{"type": "config",
				"audio":   msg{"format": "pcm_s16le", "sample_rate": int64(48000), "channels": int64(1)},
				"results": msg{"diarize": false, "drafts": true}},
		},
		{
			name: "every field",
			params: &ASRRealtimeParams{
				SampleRate:    8000,
				TurnDetection: &ASRTurnDetection{Mode: ASRTurnDetectionVAD, Eager: Bool(false), SilenceTimeoutMS: 900},
				Results: &ASRResults{Drafts: Bool(true), IgnoreTimestamps: Bool(false), Diarize: Bool(true),
					TagAudioEvents: Bool(false), VADEvents: Bool(true), EOTEvents: Bool(true)},
				Context: &ASRContext{
					History: []ASRContextItem{{Role: ASRContextRoleAssistant, Text: "Hi."}, {Role: ASRContextRoleUser, Text: "Hello."}},
					Terms:   []string{"Fish Audio"},
				},
				ClientReferenceID: "call-42",
			},
			want: msg{"type": "config",
				"audio":          msg{"format": "pcm_s16le", "sample_rate": int64(8000), "channels": int64(1)},
				"turn_detection": msg{"mode": "vad", "eager": false, "silence_timeout_ms": int64(900)},
				"results": msg{"drafts": true, "ignore_timestamps": false, "diarize": true,
					"tag_audio_events": false, "vad_events": true, "eot_events": true},
				"context": msg{
					"history": []interface{}{msg{"role": "assistant", "text": "Hi."}, msg{"role": "user", "text": "Hello."}},
					"terms":   []interface{}{"Fish Audio"}},
				"client_reference_id": "call-42"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make(chan msg, 1)
			f := newFakeASRServer(t, func(c *fakeASRConn) {
				config := c.start()
				got <- normalize(config)
				c.serve2()
			})
			stream := mustStream(t, f, context.Background(), audioOf(), tt.params)
			collect(t, stream)
			if config := <-got; !reflect.DeepEqual(config, tt.want) {
				t.Errorf("config =\n %v\nwant\n %v", config, tt.want)
			}
		})
	}
}

// serve2 finishes a session that start() already opened.
func (c *fakeASRConn) serve2() {
	for {
		m, _, ok := c.recv()
		if !ok {
			return
		}
		if m["type"] == "audio_done" {
			c.done()
			return
		}
	}
}

// normalize widens every integer to int64, so that decoded messages compare
// equal whatever MessagePack integer size they were sent in.
func normalize(v interface{}) msg {
	var walk func(v interface{}) interface{}
	walk = func(v interface{}) interface{} {
		switch x := v.(type) {
		case map[string]interface{}:
			out := msg{}
			for k, e := range x {
				out[k] = walk(e)
			}
			return out
		case []interface{}:
			out := make([]interface{}, len(x))
			for i, e := range x {
				out[i] = walk(e)
			}
			return out
		}
		if n, ok := asInt(v); ok {
			return n
		}
		return v
	}
	m, _ := walk(v).(msg)
	return m
}

func TestASRRealtime_AudioIsMsgpackBin(t *testing.T) {
	asrTestDeadline(t)
	var (
		mu     sync.Mutex
		frames [][]byte
	)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		c.serve(func(m msg, raw []byte) {
			if m["type"] == "audio" {
				mu.Lock()
				frames = append(frames, raw)
				mu.Unlock()
			}
		})
	})

	// 40 ms needs bin 16, a single byte bin 8. Empty chunks are not sent.
	chunk := pcm(40)
	stream := mustStream(t, f, context.Background(), audioOf(chunk, nil, []byte{}, []byte{0x7f}), nil)
	events := collect(t, stream)
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	for _, e := range events {
		// The fake server answers audio that is not bin as the real one does.
		if e, ok := e.(*ASRErrorEvent); ok {
			t.Errorf("server refused a message: %+v", e)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 2 {
		t.Fatalf("server got %d audio messages, want 2 (empty chunks skipped)", len(frames))
	}
	// {"type": "audio", "data": bin}: fixmap(2), fixstr "type", fixstr "audio", fixstr "data", then bin.
	prefix := []byte{0x82, 0xa4, 't', 'y', 'p', 'e', 0xa5, 'a', 'u', 'd', 'i', 'o', 0xa4, 'd', 'a', 't', 'a'}
	wantFirst := append(append(append([]byte{}, prefix...), 0xc5, byte(len(chunk)>>8), byte(len(chunk))), chunk...) // bin 16
	if !bytes.Equal(frames[0], wantFirst) {
		t.Errorf("first audio frame = % x..., want prefix % x", frames[0][:24], wantFirst[:24])
	}
	wantSecond := append(append([]byte{}, prefix...), 0xc4, 0x01, 0x7f) // bin 8
	if !bytes.Equal(frames[1], wantSecond) {
		t.Errorf("second audio frame = % x, want % x", frames[1], wantSecond)
	}
}

func TestASRRealtime_AudioWaitsForSessionStarted(t *testing.T) {
	asrTestDeadline(t)
	early := make(chan string, 1)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		config, _, ok := c.recv()
		if !ok || config["type"] != "config" {
			return
		}
		// Nothing may arrive before session_started is sent.
		_ = c.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		if _, data, err := c.conn.ReadMessage(); err == nil {
			var m msg
			_ = msgpack.Unmarshal(data, &m)
			early <- m["type"].(string)
		}
		close(early)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err := f.client().ASR.StreamWebSocket(ctx, audioOf(pcm(40)), nil, nil)
	if err == nil {
		t.Fatal("StreamWebSocket() returned a stream for a session that never started")
	}
	if got, ok := <-early; ok {
		t.Errorf("client sent %q before session_started", got)
	}
	assertNoASRGoroutines(t)
}

func TestASRRealtime_ControlMessages(t *testing.T) {
	asrTestDeadline(t)
	var (
		mu  sync.Mutex
		got []msg
	)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		c.serve(func(m msg, _ []byte) {
			if m["type"] == "audio" {
				m = msg{"type": "audio"}
			}
			mu.Lock()
			got = append(got, normalize(m))
			mu.Unlock()
		})
	})

	audio := make(chan []byte)
	stream := mustStream(t, f, context.Background(), audio, &ASRRealtimeParams{
		TurnDetection: &ASRTurnDetection{Mode: ASRTurnDetectionManual},
	})

	// Control messages keep their position in the audio stream.
	audio <- pcm(40)
	steps := []func() error{
		stream.Finalize,
		func() error { return stream.AgentSpeaking(true) },
		func() error {
			return stream.AddContext(ASRContextItem{Role: ASRContextRoleAssistant, Text: "Which day?"})
		},
		func() error { return stream.ReplaceContext(ASRContextItem{Role: ASRContextRoleUser, Text: "Friday."}) },
		func() error { return stream.AgentSpeaking(false) },
		func() error {
			return stream.Configure(&ASRConfigureParams{
				TurnDetection: &ASRTurnDetection{Mode: ASRTurnDetectionVAD, SilenceTimeoutMS: 900},
				Results:       &ASRResults{Diarize: Bool(false)},
				Terms:         []string{"Fish Audio", "Main Street"},
			})
		},
		func() error { return stream.Configure(&ASRConfigureParams{Terms: []string{}}) },
		func() error { return stream.Configure(nil) },
		stream.Keepalive,
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("control message %d: %v", i, err)
		}
	}
	audio <- pcm(40)
	close(audio)

	events := collect(t, stream)
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}

	want := []msg{
		{"type": "audio"},
		{"type": "finalize"},
		{"type": "agent_speaking", "speaking": true},
		{"type": "context_add", "items": []interface{}{msg{"role": "assistant", "text": "Which day?"}}},
		{"type": "context_add", "items": []interface{}{msg{"role": "user", "text": "Friday."}}, "reset_history": true},
		{"type": "agent_speaking", "speaking": false},
		{"type": "configure",
			"turn_detection": msg{"mode": "vad", "silence_timeout_ms": int64(900)},
			"results":        msg{"diarize": false},
			"context":        msg{"terms": []interface{}{"Fish Audio", "Main Street"}}},
		{"type": "configure", "context": msg{"terms": []interface{}{}}},
		{"type": "configure"},
		{"type": "keepalive"},
		{"type": "audio"},
		{"type": "audio_done"},
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("server got %d messages, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("message %d = %v, want %v", i, got[i], want[i])
		}
	}

	// finalize committed the first turn; the three configure calls were answered.
	types := strings.Join(eventTypes(events), " ")
	if !strings.Contains(types, "committed") || strings.Count(types, "configured") != 3 {
		t.Errorf("events = %s", types)
	}
	for _, e := range events {
		if c, ok := e.(*ASRCommittedEvent); ok && c.TurnIndex == 0 && c.Reason != "finalize" {
			t.Errorf("first turn reason = %q, want finalize", c.Reason)
		}
	}

	if err := stream.Finalize(); err == nil {
		t.Error("Finalize() after the session ended returned nil")
	} else if wsErr := (*WebSocketError)(nil); !errors.As(err, &wsErr) {
		t.Errorf("Finalize() after the session ended = %T, want *WebSocketError", err)
	}
}

// Decoding.

func TestASRRealtime_DecodesEveryEvent(t *testing.T) {
	asrTestDeadline(t)
	messages := []msg{
		{"type": "speech_start", "turn_index": 2, "audio_ms": 300},
		{"type": "user_turn_taken", "turn_index": 2, "audio_ms": 1280},
		{"type": "draft", "turn_index": 2, "text": "what's the weather", "settled_text": "what's",
			"language": "English", "language_code": "en", "audio_ms": 1800,
			"audio_received_ms": 1900, "audio_processed_ms": 1800, "audio_committed_ms": 0},
		{"type": "eager_end_of_turn", "turn_index": 2, "text": "What's the weather like?",
			"language": "English", "language_code": "en", "audio_ms": 2240},
		{"type": "turn_resumed", "turn_index": 2},
		{"type": "user_backchannel", "turn_index": 3, "audio_ms": 3840},
		{"type": "committed", "turn_index": 2, "text": "What's the weather like?",
			"words":    []msg{{"text": "What's", "start_ms": 300, "end_ms": 520}},
			"language": "English", "language_code": "en", "audio_start_ms": 300, "audio_end_ms": 2880,
			"reason": "eot", "degraded": true, "user_turn": false, "turn_kind": "backchannel",
			"audio_received_ms": 3000, "audio_processed_ms": 2900, "audio_committed_ms": 2880},
		// user_turn and reason left out, an explicit nil, and a field from the future.
		{"type": "committed", "turn_index": 3, "text": "", "language": nil, "from_the_future": msg{"x": 1}},
		{"type": "vad", "audio_ms": 1100, "score": 0.93},
		{"type": "eot_prob", "turn_index": 2, "audio_ms": 2200, "prob": 0.42},
		{"type": "configured", "ok": false, "config": msg{"turn_detection": msg{"mode": "auto"}},
			"message": "turn_detection.silence_timeout_ms must be between 3000 and 60000 in auto mode"},
		{"type": "progress", "audio_received_ms": 12000, "audio_processed_ms": 12000, "audio_committed_ms": 9600},
		{"type": "error", "code": 400, "error_type": "invalid_request", "message": "bad", "request_id": "req-9", "recoverable": true},
	}
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		for _, m := range messages {
			c.send(m)
		}
		c.serve2()
	})

	stream := mustStream(t, f, context.Background(), audioOf(), nil)
	events := collect(t, stream)
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}

	want := []ASREvent{
		&ASRSpeechStartEvent{Seq: 1, TurnIndex: 2, AudioMS: 300},
		&ASRUserTurnTakenEvent{Seq: 2, TurnIndex: 2, AudioMS: 1280},
		&ASRDraftEvent{Seq: 3, TurnIndex: 2, Text: "what's the weather", SettledText: "what's",
			Language: "English", LanguageCode: "en", AudioMS: 1800,
			AudioReceivedMS: 1900, AudioProcessedMS: 1800},
		&ASREagerEndOfTurnEvent{Seq: 4, TurnIndex: 2, Text: "What's the weather like?",
			Language: "English", LanguageCode: "en", AudioMS: 2240},
		&ASRTurnResumedEvent{Seq: 5, TurnIndex: 2},
		&ASRUserBackchannelEvent{Seq: 6, TurnIndex: 3, AudioMS: 3840},
		&ASRCommittedEvent{Seq: 7, TurnIndex: 2, Text: "What's the weather like?",
			Words:    []ASRWord{{Text: "What's", StartMS: 300, EndMS: 520}},
			Language: "English", LanguageCode: "en", AudioStartMS: 300, AudioEndMS: 2880,
			Reason: "eot", Degraded: true, UserTurn: false, TurnKind: "backchannel",
			AudioReceivedMS: 3000, AudioProcessedMS: 2900, AudioCommittedMS: 2880},
		&ASRCommittedEvent{Seq: 8, TurnIndex: 3, Reason: "eot", UserTurn: true},
		&ASRVADEvent{Seq: 9, AudioMS: 1100, Score: 0.93},
		&ASREOTProbEvent{Seq: 10, TurnIndex: 2, AudioMS: 2200, Prob: 0.42},
		&ASRConfiguredEvent{Seq: 11, OK: false,
			Config:  map[string]interface{}{"turn_detection": map[string]interface{}{"mode": "auto"}},
			Message: "turn_detection.silence_timeout_ms must be between 3000 and 60000 in auto mode"},
		&ASRProgressEvent{Seq: 12, AudioReceivedMS: 12000, AudioProcessedMS: 12000, AudioCommittedMS: 9600},
		&ASRErrorEvent{Seq: 13, Code: 400, ErrorType: "invalid_request", Message: "bad", RequestID: "req-9", Recoverable: true},
	}
	if len(events) != len(want)+2 {
		t.Fatalf("got %d events (%v), want %d", len(events), eventTypes(events), len(want)+2)
	}
	for i, w := range want {
		if got := events[i+1]; !reflect.DeepEqual(got, w) {
			t.Errorf("event %d =\n %+v\nwant\n %+v", i+1, got, w)
		}
	}
}

// Lesson 6: unknown server message types are skipped, not errors.
func TestASRRealtime_SkipsUnknownMessages(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		c.send(msg{"type": "message_from_the_future", "payload": []int{1, 2, 3}})
		c.send(msg{"note": "no type at all"})
		_ = c.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"text frame"}`))
		c.commit("still here", "eot")
		c.serve2()
	})

	stream := mustStream(t, f, context.Background(), audioOf(), nil)
	events := collect(t, stream)
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want unknown messages to be skipped", err)
	}
	want := []string{"session_started", "committed", "session_done"}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// Lesson 7: errors.

func TestASRRealtime_HandshakeRejected(t *testing.T) {
	asrTestDeadline(t)
	tests := []struct {
		status int
		check  func(err error) bool
	}{
		{401, func(err error) bool { var e *AuthenticationError; return errors.As(err, &e) }},
		{402, func(err error) bool { var e *APIError; return errors.As(err, &e) && e.StatusCode == 402 }},
		{403, func(err error) bool { var e *PermissionError; return errors.As(err, &e) }},
		{429, func(err error) bool { var e *RateLimitError; return errors.As(err, &e) }},
		{503, func(err error) bool { var e *ServerError; return errors.As(err, &e) }},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			server := newRejectingASRServer(t, tt.status, `{"message":"refused"}`)
			client := NewClient(WithAPIKey("bad-key"), WithBaseURL(server.URL))

			stream, err := client.ASR.StreamWebSocket(context.Background(), audioOf(pcm(40)), nil, nil)
			if err == nil {
				_ = stream.Close()
				t.Fatal("StreamWebSocket() error = nil")
			}
			if !tt.check(err) {
				t.Errorf("error = %T (%v), not the SDK error for HTTP %d", err, err, tt.status)
			}
			var sdkErr FishAudioError
			if !errors.As(err, &sdkErr) {
				t.Errorf("error %T is not a FishAudioError", err)
			}
			// Every status error carries the *APIError with the response body.
			var base *APIError
			switch e := err.(type) {
			case *AuthenticationError:
				base = e.APIError
			case *PermissionError:
				base = e.APIError
			case *RateLimitError:
				base = e.APIError
			case *ServerError:
				base = e.APIError
			case *APIError:
				base = e
			}
			if base == nil || base.StatusCode != tt.status || base.Body != `{"message":"refused"}` {
				t.Errorf("APIError = %+v, want status %d and the response body", base, tt.status)
			}
			assertNoASRGoroutines(t)
		})
	}
}

func TestASRRealtime_InvalidConfigIsReturnedByStreamWebSocket(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) { c.serve(nil) })

	stream, err := f.client().ASR.StreamWebSocket(context.Background(), audioOf(pcm(40)), &ASRRealtimeParams{SampleRate: 11025}, nil)
	if err == nil {
		_ = stream.Close()
		t.Fatal("StreamWebSocket() error = nil for an unsupported sample rate")
	}
	var rtErr *ASRRealtimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("error = %T (%v), want *ASRRealtimeError", err, err)
	}
	want := ASRRealtimeError{
		Message:   "audio.sample_rate must be one of 8000, 12000, 16000, 22050, 24000, 32000, 44100, 48000",
		ErrorType: "unsupported_audio", Code: 400, RequestID: "req-1", CloseCode: 4400,
	}
	if *rtErr != want {
		t.Errorf("error = %+v, want %+v", *rtErr, want)
	}
	assertNoASRGoroutines(t)
}

func TestASRRealtime_FatalErrorEvent(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		c.commit("before the failure", "eot")
		c.fail("engine_error", 500, "the engine failed", 4500)
	})

	stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
	events := collect(t, stream)

	// What the server sent before the error is delivered; the error is not an event.
	if got, want := eventTypes(events), []string{"session_started", "committed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	var rtErr *ASRRealtimeError
	if !errors.As(stream.Err(), &rtErr) {
		t.Fatalf("Err() = %T (%v), want *ASRRealtimeError", stream.Err(), stream.Err())
	}
	want := ASRRealtimeError{Message: "the engine failed", ErrorType: "engine_error", Code: 500, RequestID: "req-1", CloseCode: 4500}
	if *rtErr != want {
		t.Errorf("Err() = %+v, want %+v", *rtErr, want)
	}
	if !strings.Contains(rtErr.Error(), "engine_error") || !strings.Contains(rtErr.Error(), "4500") {
		t.Errorf("Error() = %q", rtErr.Error())
	}
	assertNoASRGoroutines(t) // without Close
}

// At the session's duration limit the server commits the open turn and closes
// with 4413. Whichever side of the error message that commit arrives on, it
// is delivered: the caller's last words are not dropped because the session
// is ending.
func TestASRRealtime_EventsAfterFatalErrorAreDelivered(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		c.send(msg{"type": "error", "code": 413, "error_type": "max_duration_exceeded", "message": "session too long",
			"request_id": "req-1", "recoverable": false})
		c.commit("please cancel my order", "finalize")
		// Nothing after the error can turn the session into a success or
		// replace the error.
		c.send(msg{"type": "error", "code": 400, "error_type": "invalid_request", "message": "late",
			"request_id": "req-2", "recoverable": true})
		c.send(msg{"type": "session_done", "stats": msg{"audio_ms": 0, "turns": 1, "request_id": "req-1"}})
		c.close(4413, "max duration exceeded")
	})
	stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
	events := collect(t, stream)
	if got, want := eventTypes(events), []string{"session_started", "committed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if c := events[1].(*ASRCommittedEvent); c.Text != "please cancel my order" || c.Reason != "finalize" {
		t.Errorf("committed = %+v", c)
	}
	var rtErr *ASRRealtimeError
	if !errors.As(stream.Err(), &rtErr) {
		t.Fatalf("Err() = %T (%v), want *ASRRealtimeError", stream.Err(), stream.Err())
	}
	want := ASRRealtimeError{Message: "session too long", ErrorType: "max_duration_exceeded", Code: 413, RequestID: "req-1", CloseCode: 4413}
	if *rtErr != want {
		t.Errorf("Err() = %+v, want %+v", *rtErr, want)
	}
	assertNoASRGoroutines(t)
}

// A fatal error is reported even when the server never closes the connection.
func TestASRRealtime_FatalErrorWithoutClose(t *testing.T) {
	asrTestDeadline(t)
	shortGrace(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		c.send(msg{"type": "error", "code": 500, "error_type": "internal", "message": "boom",
			"request_id": "req-1", "recoverable": false})
		c.commit("after the error", "eot") // still delivered
		c.waitClosed(5 * time.Second)
	})

	stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
	events := collect(t, stream)
	if got, want := eventTypes(events), []string{"session_started", "committed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	var rtErr *ASRRealtimeError
	if !errors.As(stream.Err(), &rtErr) || rtErr.ErrorType != "internal" || rtErr.CloseCode != 0 {
		t.Errorf("Err() = %#v, want *ASRRealtimeError internal without a close code", stream.Err())
	}
}

func TestASRRealtime_RecoverableErrorIsAnEvent(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		c.send(msg{"type": "error", "code": 400, "error_type": "invalid_request", "message": "dropped",
			"request_id": "req-1", "recoverable": true})
		c.commit("the session goes on", "eot")
		c.serve2()
	})

	stream := mustStream(t, f, context.Background(), audioOf(), nil)
	events := collect(t, stream)
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil after a recoverable error", err)
	}
	want := []string{"session_started", "error", "committed", "session_done"}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if e := events[1].(*ASRErrorEvent); e.ErrorType != "invalid_request" || !e.Recoverable || e.Code != 400 {
		t.Errorf("error event = %+v", e)
	}
}

func TestASRRealtime_AbnormalClose(t *testing.T) {
	asrTestDeadline(t)
	tests := []struct {
		name   string
		script func(c *fakeASRConn)
		want   ASRRealtimeError
		events []string
	}{
		{
			name: "idle timeout closes with 4408 and no error message",
			script: func(c *fakeASRConn) {
				c.close(4408, "idle timeout")
			},
			want:   ASRRealtimeError{Message: "idle timeout", CloseCode: 4408},
			events: []string{"session_started"},
		},
		{
			// Recoverable, yet the connection closes: the turn is committed in between.
			name: "server_restarting closes with 1012",
			script: func(c *fakeASRConn) {
				c.send(msg{"type": "error", "code": 503, "error_type": "server_restarting", "message": "restarting",
					"request_id": "req-1", "recoverable": true})
				c.commit("last words", "eot")
				c.close(1012, "")
			},
			want:   ASRRealtimeError{Message: "restarting", ErrorType: "server_restarting", Code: 503, RequestID: "req-1", CloseCode: 1012},
			events: []string{"session_started", "error", "committed"},
		},
		{
			// An earlier recoverable error does not explain a later, unrelated close.
			name: "stale recoverable error is not blamed",
			script: func(c *fakeASRConn) {
				c.send(msg{"type": "error", "code": 400, "error_type": "invalid_request", "message": "dropped",
					"request_id": "req-1", "recoverable": true})
				c.commit("fine", "eot")
				c.close(1011, "internal error")
			},
			want:   ASRRealtimeError{Message: "internal error", CloseCode: 1011},
			events: []string{"session_started", "error", "committed"},
		},
		{
			// 1000 is the code of a normal end, but only after session_done.
			name: "closed with 1000 before session_done",
			script: func(c *fakeASRConn) {
				c.send(msg{"type": "draft", "turn_index": 0, "text": "half a sent", "settled_text": "", "audio_ms": 0})
				c.close(websocket.CloseNormalClosure, "")
			},
			want:   ASRRealtimeError{Message: "connection closed before session_done", CloseCode: websocket.CloseNormalClosure},
			events: []string{"session_started", "draft"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeASRServer(t, func(c *fakeASRConn) {
				if c.start() != nil {
					tt.script(c)
				}
			})
			stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
			events := collect(t, stream)
			if got := eventTypes(events); !reflect.DeepEqual(got, tt.events) {
				t.Errorf("events = %v, want %v", got, tt.events)
			}
			var rtErr *ASRRealtimeError
			if !errors.As(stream.Err(), &rtErr) {
				t.Fatalf("Err() = %T (%v), want *ASRRealtimeError", stream.Err(), stream.Err())
			}
			if *rtErr != tt.want {
				t.Errorf("Err() = %+v, want %+v", *rtErr, tt.want)
			}
			assertNoASRGoroutines(t) // without Close
		})
	}
}

// Lesson 1: a network failure while the session opens is an SDK error.
// A connection that drops without a close frame is a lost connection, not a
// session the server ended: *WebSocketError, with the cause reachable. The
// WebSocket library reports it as a close with code 1006, which is never sent
// by a server.
func TestASRRealtime_DroppedConnection(t *testing.T) {
	asrTestDeadline(t)
	t.Run("nothing before it", func(t *testing.T) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() != nil {
				_ = c.conn.UnderlyingConn().Close()
			}
		})
		stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
		if got, want := eventTypes(collect(t, stream)), []string{"session_started"}; !reflect.DeepEqual(got, want) {
			t.Errorf("events = %v, want %v", got, want)
		}
		err := stream.Err()
		var wsErr *WebSocketError
		if !errors.As(err, &wsErr) {
			t.Fatalf("Err() = %T (%v), want *WebSocketError", err, err)
		}
		var rtErr *ASRRealtimeError
		if errors.As(err, &rtErr) {
			t.Errorf("Err() = %v is also an *ASRRealtimeError", err)
		}
		var closeErr *websocket.CloseError
		if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseAbnormalClosure {
			t.Errorf("the cause is not reachable through Unwrap: %v", err)
		}
		assertNoASRGoroutines(t)
	})
	// After an error that ends the session, the server's error is the
	// result: there is just no close code to go with it.
	t.Run("after a fatal error", func(t *testing.T) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() == nil {
				return
			}
			c.send(msg{"type": "error", "code": 500, "error_type": "internal", "message": "boom",
				"request_id": "req-1", "recoverable": false})
			_ = c.conn.UnderlyingConn().Close()
		})
		stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
		collect(t, stream)
		var rtErr *ASRRealtimeError
		if !errors.As(stream.Err(), &rtErr) {
			t.Fatalf("Err() = %T (%v), want *ASRRealtimeError", stream.Err(), stream.Err())
		}
		if want := (ASRRealtimeError{Message: "boom", ErrorType: "internal", Code: 500, RequestID: "req-1"}); *rtErr != want {
			t.Errorf("Err() = %+v, want %+v", *rtErr, want)
		}
	})
}

// A control message goes ahead of the audio still waiting in the channel.
// The writer is held in the write of a chunk too large for a server that is
// not reading; Finalize is then waiting to be picked up, and after that one
// more chunk is put in the channel. When the server reads, finalize must come
// before that chunk, every time: the writer may not choose between the two.
func TestASRRealtime_ControlGoesAheadOfWaitingAudio(t *testing.T) {
	asrTestDeadline(t)
	// finalizeWaiting reports whether a Finalize call is parked at handing
	// its message to the writer.
	finalizeWaiting := func() bool {
		buf := make([]byte, 1<<20)
		for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(g, "(*ASRRealtimeStream).send(") && strings.HasPrefix(g, "goroutine ") &&
				strings.Contains(strings.SplitN(g, "\n", 2)[0], "[select") {
				return true
			}
		}
		return false
	}
	for i := 0; i < 10; i++ {
		read := make(chan struct{})
		order := make(chan []string, 1)
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() == nil {
				return
			}
			<-read
			var got []string
			for len(got) < 3 {
				m, _, ok := c.recv()
				if !ok {
					break
				}
				kind := m["type"].(string)
				if data, isAudio := m["data"].([]byte); isAudio {
					kind = fmt.Sprintf("audio(%d)", len(data))
				}
				got = append(got, kind)
			}
			order <- got
		})
		audio := make(chan []byte, 1)
		stream := mustStream(t, f, context.Background(), audio, nil)

		big := make([]byte, 16<<20) // more than the socket buffers hold
		audio <- big
		within(t, "the writer to take the first chunk", func() {
			for len(audio) > 0 {
				time.Sleep(time.Millisecond)
			}
		})
		finalized := make(chan error, 1)
		go func() { finalized <- stream.Finalize() }()
		within(t, "Finalize to wait for the writer", func() {
			for !finalizeWaiting() {
				time.Sleep(time.Millisecond)
			}
		})
		audio <- pcm(40) // waits in the channel, behind the pending finalize
		close(read)

		var got []string
		within(t, "the server to read three messages", func() { got = <-order })
		want := []string{fmt.Sprintf("audio(%d)", len(big)), "finalize", fmt.Sprintf("audio(%d)", len(pcm(40)))}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: server got %v, want %v", i, got, want)
		}
		if err := <-finalized; err != nil {
			t.Fatalf("Finalize() = %v", err)
		}
		_ = stream.Close()
		f.Close()
	}
	assertNoASRGoroutines(t)
}

// A write that fails ends the session, whichever write it was. The reader
// cannot be relied on to notice: a connection can refuse writes while a read
// on it just keeps waiting. Here the client's sending side is shut, and the
// server neither reads nor closes.
func TestASRRealtime_FailedWriteEndsSession(t *testing.T) {
	asrTestDeadline(t)
	shortGrace(t)
	start := func(t *testing.T, opts *WebSocketOptions) *ASRRealtimeStream {
		release := make(chan struct{})
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() != nil {
				<-release
			}
		})
		t.Cleanup(func() { close(release) }) // runs before the server's cleanup
		stream, err := f.client().ASR.StreamWebSocket(context.Background(), make(chan []byte), nil, opts)
		if err != nil {
			t.Fatalf("StreamWebSocket() error = %v", err)
		}
		if err := stream.conn.NetConn().(*net.TCPConn).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		return stream
	}
	ended := func(t *testing.T, stream *ASRRealtimeStream) {
		t.Helper()
		collect(t, stream) // returns once the session has ended
		var wsErr *WebSocketError
		if !errors.As(stream.Err(), &wsErr) {
			t.Errorf("Err() = %T (%v), want *WebSocketError", stream.Err(), stream.Err())
		}
		assertNoASRGoroutines(t)
	}

	t.Run("control message", func(t *testing.T) {
		opts := DefaultWebSocketOptions()
		opts.PingInterval = 0
		stream := start(t, opts)
		if err := stream.Finalize(); err == nil {
			t.Fatal("Finalize() = nil on a connection that cannot be written to")
		}
		ended(t, stream)
		if err := stream.Finalize(); err == nil {
			t.Error("Finalize() = nil after the session ended")
		}
	})
	t.Run("ping", func(t *testing.T) {
		opts := DefaultWebSocketOptions()
		opts.PingInterval = 10 * time.Millisecond
		ended(t, start(t, opts))
	})
}

func TestASRRealtime_ConnectionFailuresAreSDKErrors(t *testing.T) {
	asrTestDeadline(t)
	isSDKError := func(err error) bool {
		var sdkErr FishAudioError
		return errors.As(err, &sdkErr)
	}

	t.Run("connection refused", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		_ = listener.Close()

		client := NewClient(WithAPIKey("k"), WithBaseURL("http://"+addr))
		_, err = client.ASR.StreamWebSocket(context.Background(), audioOf(), nil, nil)
		var wsErr *WebSocketError
		if !errors.As(err, &wsErr) {
			t.Fatalf("error = %T (%v), want *WebSocketError", err, err)
		}
		var opErr *net.OpError
		if !errors.As(err, &opErr) {
			t.Errorf("the cause is not reachable through Unwrap: %v", err)
		}
	})

	t.Run("dropped right after the upgrade, around the config message", func(t *testing.T) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			_ = c.conn.UnderlyingConn().Close()
		})
		_, err := f.client().ASR.StreamWebSocket(context.Background(), audioOf(pcm(40)), nil, nil)
		if err == nil || !isSDKError(err) {
			t.Fatalf("error = %T (%v), want an SDK error", err, err)
		}
		assertNoASRGoroutines(t)
	})

	t.Run("closed normally before session_started", func(t *testing.T) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			c.recv()
			c.close(websocket.CloseNormalClosure, "")
		})
		_, err := f.client().ASR.StreamWebSocket(context.Background(), audioOf(pcm(40)), nil, nil)
		if err == nil || !isSDKError(err) {
			t.Fatalf("error = %T (%v), want an SDK error", err, err)
		}
		assertNoASRGoroutines(t)
	})

	t.Run("malformed server message", func(t *testing.T) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() == nil {
				return
			}
			_ = c.conn.WriteMessage(websocket.BinaryMessage, []byte{0xc1}) // never valid MessagePack
			c.waitClosed(2 * time.Second)
		})
		stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
		collect(t, stream)
		var wsErr *WebSocketError
		if !errors.As(stream.Err(), &wsErr) {
			t.Fatalf("Err() = %T (%v), want *WebSocketError", stream.Err(), stream.Err())
		}
	})

	t.Run("nil audio channel", func(t *testing.T) {
		client := NewClient(WithAPIKey("k"), WithBaseURL("http://127.0.0.1:1"))
		if _, err := client.ASR.StreamWebSocket(context.Background(), nil, nil, nil); err == nil {
			t.Fatal("StreamWebSocket(nil audio) error = nil")
		}
	})
}

// Lesson 5: the connection is closed by the time session_done is handed out.
func TestASRRealtime_ConnectionClosedAtSessionDone(t *testing.T) {
	asrTestDeadline(t)
	closedByClient := make(chan bool, 1)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		for {
			m, _, ok := c.recv()
			if !ok {
				return
			}
			if m["type"] == "audio_done" {
				break
			}
		}
		// A server that sends session_done but leaves the connection open.
		c.send(msg{"type": "session_done", "stats": msg{"audio_ms": 0, "turns": 0}})
		closedByClient <- c.waitClosed(3 * time.Second)
	})

	stream, err := f.client().ASR.StreamWebSocket(context.Background(), audioOf(), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	// The caller stops at session_done and never calls Close.
	sawDone := false
	within(t, "reading up to session_done", func() {
		for stream.Next() {
			if _, ok := stream.Event().(*ASRSessionDoneEvent); ok {
				sawDone = true
				// Already closed: a control message is refused.
				if err := stream.Keepalive(); err == nil {
					t.Error("Keepalive() at session_done = nil, want the session to be closed")
				}
				return
			}
		}
	})
	if !sawDone {
		t.Fatalf("no session_done; Err() = %v", stream.Err())
	}
	select {
	case closed := <-closedByClient:
		if !closed {
			t.Error("the client left the connection open after session_done")
		}
	case <-time.After(asrTestWait):
		t.Fatal("server did not finish")
	}
	assertNoASRGoroutines(t) // without Close
	if stream.Next() {
		t.Error("Next() = true after session_done")
	}
	if err := stream.Err(); err != nil {
		t.Errorf("Err() = %v", err)
	}
}

// Lesson 2: Close wakes a consumer that is waiting for the next event.
func TestASRRealtime_CloseWakesBlockedNext(t *testing.T) {
	asrTestDeadline(t)
	serverSawClose := make(chan bool, 1)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		serverSawClose <- c.waitClosed(5 * time.Second) // a silent session
	})

	stream := mustStream(t, f, context.Background(), make(chan []byte), nil)
	if !stream.Next() {
		t.Fatalf("no session_started: %v", stream.Err())
	}

	result := make(chan bool, 1)
	go func() { result <- stream.Next() }()
	time.Sleep(50 * time.Millisecond) // let it block
	select {
	case <-result:
		t.Fatal("Next() returned before Close")
	default:
	}

	within(t, "Close", func() { _ = stream.Close() })
	select {
	case got := <-result:
		if got {
			t.Error("Next() = true after Close")
		}
	case <-time.After(asrTestWait):
		t.Fatal("Next() is still blocked after Close")
	}
	if err := stream.Err(); err != nil {
		t.Errorf("Err() = %v, want nil after Close", err)
	}
	if !<-serverSawClose {
		t.Error("server did not see the connection close")
	}

	// Closed for good, and safe to close again.
	if err := stream.Finalize(); err == nil {
		t.Error("Finalize() after Close = nil")
	}
	within(t, "second Close", func() { _ = stream.Close() })
	assertNoASRGoroutines(t)
}

// floodServer starts a session and then sends drafts as fast as it can until
// the client goes away. sent counts them.
func floodServer(t *testing.T, sent *atomic.Int64, textSize int) *fakeASRServer {
	text := strings.Repeat("x", textSize)
	return newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		go func() { // keep reading, so that client writes never stall the test
			for {
				if _, _, err := c.conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		for c.send(msg{"type": "draft", "turn_index": 0, "text": text, "settled_text": "", "audio_ms": 0}) {
			sent.Add(1)
		}
	})
}

// Lesson 3: when the audio source fails (the caller cancels ctx with its
// error) the session ends with that error, even while the server keeps
// sending events.
func TestASRRealtime_AudioSourceFailureEndsSession(t *testing.T) {
	asrTestDeadline(t)
	errMicrophone := errors.New("microphone unplugged")

	t.Run("consumer reading a busy stream", func(t *testing.T) {
		var sent atomic.Int64
		f := floodServer(t, &sent, 64)
		ctx, fail := context.WithCancelCause(context.Background())
		defer fail(nil)
		audio := make(chan []byte)
		stream := mustStream(t, f, ctx, audio, nil)

		go func() { // the audio source: two chunks, then it breaks
			audio <- pcm(40)
			audio <- pcm(40)
			fail(errMicrophone)
		}()

		start := time.Now()
		n := 0
		within(t, "reading a stream whose audio source failed", func() {
			for stream.Next() {
				n++
			}
		})
		if err := stream.Err(); !errors.Is(err, errMicrophone) {
			t.Fatalf("Err() = %v, want the audio source's error", err)
		}
		t.Logf("ended after %d events in %v", n, time.Since(start))
		// Nothing is delivered after the failure, however much is buffered.
		if stream.Next() {
			t.Error("Next() = true after the failure")
		}
	})

	t.Run("consumer waiting on a silent stream", func(t *testing.T) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() != nil {
				c.waitClosed(5 * time.Second)
			}
		})
		ctx, fail := context.WithCancelCause(context.Background())
		defer fail(nil)
		stream := mustStream(t, f, ctx, make(chan []byte), nil)
		if !stream.Next() {
			t.Fatal("no session_started")
		}
		time.AfterFunc(50*time.Millisecond, func() { fail(errMicrophone) })
		within(t, "Next after the failure", func() {
			if stream.Next() {
				t.Error("Next() = true")
			}
		})
		if err := stream.Err(); !errors.Is(err, errMicrophone) {
			t.Fatalf("Err() = %v, want the audio source's error", err)
		}
	})

	// The failure is not lost when the audio was already finished and the
	// server is about to close normally.
	t.Run("after the audio channel is closed", func(t *testing.T) {
		release := make(chan struct{})
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() == nil {
				return
			}
			for {
				m, _, ok := c.recv()
				if !ok {
					return
				}
				if m["type"] == "audio_done" {
					break
				}
			}
			<-release
			c.done() // a normal end, too late
		})
		defer close(release)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream := mustStream(t, f, ctx, audioOf(pcm(40)), nil)
		if !stream.Next() {
			t.Fatal("no session_started")
		}
		time.AfterFunc(50*time.Millisecond, cancel)
		within(t, "Next after cancel", func() {
			for stream.Next() {
			}
		})
		if err := stream.Err(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Err() = %v, want context.Canceled", err)
		}
	})

	t.Run("while waiting for session_started", func(t *testing.T) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			c.recv()
			c.waitClosed(5 * time.Second) // never answers config
		})
		ctx, fail := context.WithCancelCause(context.Background())
		time.AfterFunc(50*time.Millisecond, func() { fail(errMicrophone) })
		var err error
		within(t, "StreamWebSocket", func() {
			_, err = f.client().ASR.StreamWebSocket(ctx, make(chan []byte), nil, nil)
		})
		if !errors.Is(err, errMicrophone) {
			t.Fatalf("error = %v, want the cancellation cause", err)
		}
		assertNoASRGoroutines(t)
	})
}

// Ending a session is a race between its normal end and ctx: the watch on ctx
// is released only after the session is marked as ended, so the callback can
// already be running by then. Whichever ends the session decides what happens
// to the events not yet read. If the normal end won, they are all delivered,
// session_done last, and Err is nil: a ctx that came second changes nothing.
func TestASRRealtime_CtxDoneAfterNormalEndKeepsEvents(t *testing.T) {
	asrTestDeadline(t)
	want := []string{"session_started", "speech_start", "draft", "committed", "session_done"}

	// ended returns a session that ended normally with nothing read yet, and
	// whose goroutines have returned.
	ended := func(t *testing.T) (*ASRRealtimeStream, context.Context) {
		f := newFakeASRServer(t, func(c *fakeASRConn) { c.serve(nil) })
		ctx, cancel := context.WithCancel(context.Background())
		stream := mustStream(t, f, ctx, audioOf(pcm(40)), nil)
		within(t, "the end of the session", func() {
			<-stream.done
			stream.wg.Wait()
		})
		cancel()
		return stream, ctx
	}
	delivered := func(t *testing.T, stream *ASRRealtimeStream) {
		t.Helper()
		if got := eventTypes(collect(t, stream)); !reflect.DeepEqual(got, want) {
			t.Errorf("events = %v, want %v", got, want)
		}
		if err := stream.Err(); err != nil {
			t.Errorf("Err() = %v, want nil: the session ended normally", err)
		}
	}

	// The callback of the watch on ctx, started just before the watch was
	// released: it runs with the session already ended. Calling it here is
	// that interleaving, without having to hit the window.
	t.Run("ctx callback", func(t *testing.T) {
		stream, ctx := ended(t)
		stream.ctxDone(ctx)
		delivered(t, stream)
	})
	// The same for the other path that ends a session from this side.
	t.Run("failed send", func(t *testing.T) {
		stream, _ := ended(t)
		stream.sendFailed(errors.New("write on a closed connection"))
		delivered(t, stream)
	})
	// Close is the caller's own decision and still discards them.
	t.Run("Close", func(t *testing.T) {
		stream, _ := ended(t)
		_ = stream.Close()
		if stream.Next() {
			t.Errorf("Next() = true after Close: %T", stream.Event())
		}
		if err := stream.Err(); err != nil {
			t.Errorf("Err() = %v", err)
		}
	})
}

// When ctx or a failed send ends a session, the session is marked as ended
// and its unread events are dropped in one step. Otherwise there is a moment
// in which Err already reports the failure while Next still hands out a
// buffered event. The hook looks at the session at the instant it is marked
// as ended, from the goroutine that ended it.
func TestASRRealtime_EndedAndDiscardedTogether(t *testing.T) {
	asrTestDeadline(t)
	errSend := errors.New("write on a closed connection")
	tests := []struct {
		name string
		end  func(stream *ASRRealtimeStream, cancel context.CancelFunc)
		want error
	}{
		{"ctx", func(_ *ASRRealtimeStream, cancel context.CancelFunc) { cancel() }, context.Canceled},
		{"failed send", func(stream *ASRRealtimeStream, _ context.CancelFunc) { go stream.abort(errSend) }, errSend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			f := newFakeASRServer(t, func(c *fakeASRConn) {
				if c.start() == nil {
					return
				}
				for i := 0; i < 3; i++ {
					c.send(msg{"type": "draft", "turn_index": 0, "text": "x", "settled_text": "", "audio_ms": 0})
				}
				<-release
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := mustStream(t, f, ctx, make(chan []byte), nil)
			// Unread events are waiting: session_started and the drafts.
			within(t, "the events to arrive", func() {
				for len(stream.events) < 4 {
					time.Sleep(time.Millisecond)
				}
			})

			type seen struct {
				err  error
				next bool
			}
			atDone := make(chan seen, 1)
			asrAfterDone = func(s *ASRRealtimeStream) { atDone <- seen{s.Err(), s.Next()} }
			defer func() { asrAfterDone = nil }()

			tt.end(stream, cancel)
			var got seen
			within(t, "the session to end", func() { got = <-atDone })
			if !errors.Is(got.err, tt.want) {
				t.Errorf("Err() at the end = %v, want %v", got.err, tt.want)
			}
			if got.next {
				t.Errorf("Next() = true (%T) once the session had ended with %v", stream.Event(), got.err)
			}
			within(t, "the session goroutines", stream.wg.Wait)
			assertNoASRGoroutines(t)
		})
	}
}

// The real race, many times: ctx is canceled just as the server ends the
// session. Either may win, and each outcome must be whole: the ctx error
// with the rest dropped, or every event up to session_done with no error.
// Never a session cut short that reports success.
func TestASRRealtime_CtxDoneRacingNormalEnd(t *testing.T) {
	asrTestDeadline(t)
	want := []string{"session_started", "speech_start", "draft", "committed", "session_done"}
	// One server for every run: each script takes the run it belongs to.
	type run struct {
		i      int
		cancel context.CancelFunc
	}
	runs := make(chan run, 1)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		r := <-runs
		if c.start() == nil {
			return
		}
		for {
			m, _, ok := c.recv()
			if !ok {
				return
			}
			if m["type"] == "audio_done" {
				break
			}
		}
		c.send(msg{"type": "speech_start", "turn_index": 0, "audio_ms": 0})
		c.send(msg{"type": "draft", "turn_index": 0, "text": "hello", "settled_text": "", "audio_ms": 40})
		c.commit("hello", "audio_done")
		// Cancel around session_done, at a different point on each run:
		// before it, right after it, a few scheduler turns later (while the
		// client is ending the session), or once the client is done.
		if r.i%4 == 0 {
			go r.cancel()
		}
		c.send(msg{"type": "session_done", "stats": msg{"audio_ms": 40, "turns": 1, "request_id": "req-1"}})
		switch r.i % 4 {
		case 1:
			go r.cancel()
		case 2:
			go func() {
				for n := 0; n < r.i; n++ {
					runtime.Gosched()
				}
				r.cancel()
			}()
		}
		c.close(websocket.CloseNormalClosure, "done")
		r.cancel()
	})

	var normal, canceled int
	for i := 0; i < 60; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		runs <- run{i, cancel}
		stream := mustStream(t, f, ctx, audioOf(pcm(40)), nil)
		got := eventTypes(collect(t, stream))
		err := stream.Err()
		switch {
		case err == nil:
			normal++
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("run %d: Err() = nil with events %v, want %v", i, got, want)
			}
		case errors.Is(err, context.Canceled):
			canceled++
		default:
			t.Fatalf("run %d: Err() = %v", i, err)
		}
		cancel()
	}
	assertNoASRGoroutines(t)
	t.Logf("normal end won %d times, ctx %d times", normal, canceled)
}

// A session that ended normally is not turned into a failure by a later cancel.
func TestASRRealtime_CancelAfterNormalEnd(t *testing.T) {
	asrTestDeadline(t)
	f := newFakeASRServer(t, func(c *fakeASRConn) { c.serve(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	stream := mustStream(t, f, ctx, audioOf(pcm(40)), nil)
	events := collect(t, stream)
	cancel()
	assertNoASRGoroutines(t)
	if err := stream.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
	if n := len(events); n == 0 || events[n-1].EventType() != "session_done" {
		t.Errorf("events = %v", eventTypes(events))
	}
}

// Lesson 4: a slow consumer gets backpressure, not an unbounded buffer, and
// Close does not hang on the full buffer.
func TestASRRealtime_BackpressureAndCloseWithFullBuffer(t *testing.T) {
	asrTestDeadline(t)
	var sent atomic.Int64
	f := floodServer(t, &sent, 16<<10) // 16 KiB per event
	stream, err := f.client().ASR.StreamWebSocket(context.Background(), make(chan []byte), nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}

	// The consumer reads nothing. The server must stall: wait until its
	// counter stops moving.
	var stalledAt int64
	deadline := time.Now().Add(10 * time.Second)
	for {
		before := sent.Load()
		time.Sleep(200 * time.Millisecond)
		// The buffer must be full as well: on a loaded machine the server
		// can stand still for that long without having stalled.
		if after := sent.Load(); after == before && after > 0 && len(stream.events) == asrEventBuffer {
			stalledAt = after
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server is still sending after %d events: the client buffers without limit", sent.Load())
		}
	}
	if buffered := len(stream.events); buffered != asrEventBuffer {
		t.Errorf("buffered events = %d, want the buffer full at %d", buffered, asrEventBuffer)
	}
	t.Logf("server stalled after %d events (%d MiB); %d held by the SDK", stalledAt, stalledAt*16>>10, len(stream.events))

	// The events are intact and in order once the consumer reads again.
	for i := 0; i < 3; i++ {
		if !stream.Next() {
			t.Fatalf("Next() = false: %v", stream.Err())
		}
	}
	if d, ok := stream.Event().(*ASRDraftEvent); !ok || d.Seq != 2 {
		t.Errorf("third event = %+v, want the draft with seq 2", stream.Event())
	}

	// Let the buffer fill up again, then close: the reader is parked on it.
	time.Sleep(200 * time.Millisecond)
	within(t, "Close with a full buffer", func() { _ = stream.Close() })
	assertNoASRGoroutines(t)
	if stream.Next() {
		t.Error("Next() = true after Close")
	}
	if err := stream.Err(); err != nil {
		t.Errorf("Err() = %v", err)
	}
}

// Close also returns while a send is stuck on a server that stopped reading.
func TestASRRealtime_CloseWhileSendIsBlocked(t *testing.T) {
	asrTestDeadline(t)
	release := make(chan struct{})
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() != nil {
			<-release // never reads again
		}
	})
	defer close(release)

	audio := make(chan []byte)
	stream, err := f.client().ASR.StreamWebSocket(context.Background(), audio, nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	stop := make(chan struct{})
	var pushed atomic.Int64
	go func() {
		chunk := make([]byte, 256<<10)
		for {
			select {
			case audio <- chunk:
				pushed.Add(1)
			case <-stop:
				return
			}
		}
	}()
	defer close(stop)

	// Wait until the socket is full and the sender is stuck in a write.
	deadline := time.Now().Add(10 * time.Second)
	for {
		before := pushed.Load()
		time.Sleep(200 * time.Millisecond)
		if pushed.Load() == before && before > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sender never stalled")
		}
	}
	within(t, "Close while a send is blocked", func() { _ = stream.Close() })
	assertNoASRGoroutines(t)
}

// A session can end normally while the event buffer is full: session_done
// arrives, the connection is closed, and the watch on ctx stops with it. The
// reader must not wait for room in the buffer then, where neither ctx nor
// anything but Close would release it. The session still ended well: Err is
// nil, and a consumer that reads on gets every event, session_done last.
func TestASRRealtime_SessionDoneWithFullBuffer(t *testing.T) {
	asrTestDeadline(t)
	// session_started and the drafts fill the buffer exactly; session_done is
	// the one event more.
	const total = asrEventBuffer + 1
	start := func(t *testing.T) (*ASRRealtimeStream, context.CancelFunc) {
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			if c.start() == nil {
				return
			}
			for i := 1; i < asrEventBuffer; i++ {
				c.send(msg{"type": "draft", "turn_index": 0, "text": "x", "settled_text": "", "audio_ms": 0})
			}
			c.done()
		})
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		stream, err := f.client().ASR.StreamWebSocket(ctx, make(chan []byte), nil, nil)
		if err != nil {
			t.Fatalf("StreamWebSocket() error = %v", err)
		}
		// Nothing is read: the session ends with the buffer full.
		select {
		case <-stream.done:
		case <-time.After(asrTestWait):
			t.Fatal("the session did not end")
		}
		if buffered := len(stream.events); buffered != asrEventBuffer {
			t.Fatalf("buffered events = %d, want the buffer full at %d", buffered, asrEventBuffer)
		}
		return stream, cancel
	}
	// released fails the test unless both session goroutines have returned.
	released := func(t *testing.T, stream *ASRRealtimeStream, when string) {
		t.Helper()
		exited := make(chan struct{})
		go func() {
			stream.wg.Wait()
			close(exited)
		}()
		select {
		case <-exited:
		case <-time.After(asrTestWait):
			t.Fatalf("the reader is still waiting on the full buffer %s", when)
		}
		assertNoASRGoroutines(t)
	}
	readAll := func(t *testing.T, stream *ASRRealtimeStream) {
		t.Helper()
		var events []ASREvent
		within(t, "reading the events", func() {
			for stream.Next() {
				events = append(events, stream.Event())
			}
		})
		if len(events) != total {
			t.Fatalf("got %d events, want %d", len(events), total)
		}
		if _, ok := events[0].(*ASRSessionStartedEvent); !ok {
			t.Errorf("first event = %T, want *ASRSessionStartedEvent", events[0])
		}
		for i, event := range events[1 : total-1] {
			if d, ok := event.(*ASRDraftEvent); !ok || d.Seq != i+1 {
				t.Fatalf("event %d = %+v, want the draft with seq %d", i+1, event, i+1)
			}
		}
		if _, ok := events[total-1].(*ASRSessionDoneEvent); !ok {
			t.Errorf("last event = %T, want *ASRSessionDoneEvent", events[total-1])
		}
		if stream.Next() {
			t.Error("Next() = true after the last event")
		}
		if err := stream.Err(); err != nil {
			t.Errorf("Err() = %v, want nil: the session ended normally", err)
		}
	}

	t.Run("consumer gone, ctx canceled", func(t *testing.T) {
		stream, cancel := start(t)
		cancel()
		released(t, stream, "after ctx was canceled")
		if err := stream.Err(); err != nil {
			t.Errorf("Err() = %v, want nil: the session ended normally", err)
		}
	})
	t.Run("consumer gone, nothing called", func(t *testing.T) {
		stream, _ := start(t)
		released(t, stream, "after the session ended")
	})
	t.Run("consumer reads on", func(t *testing.T) {
		stream, _ := start(t)
		readAll(t, stream)
		released(t, stream, "after every event was read")
	})
	t.Run("consumer reads on after ctx was canceled", func(t *testing.T) {
		stream, cancel := start(t)
		cancel()
		released(t, stream, "after ctx was canceled")
		readAll(t, stream)
	})
	t.Run("Close", func(t *testing.T) {
		stream, _ := start(t)
		within(t, "Close", func() { _ = stream.Close() })
		assertNoASRGoroutines(t)
		if stream.Next() {
			t.Error("Next() = true after Close")
		}
		if err := stream.Err(); err != nil {
			t.Errorf("Err() = %v", err)
		}
	})
}

// The first write of a session, the config message, is covered by ctx like
// every later one: the dial deadline is gone once the upgrade has succeeded,
// so a peer that stops reading right after it must not hang StreamWebSocket.
func TestASRRealtime_CancelWhileConfigWriteIsBlocked(t *testing.T) {
	asrTestDeadline(t)
	errMicrophone := errors.New("microphone unplugged")
	// Each ctx ends only after the server has upgraded the connection, so
	// that it ends during the config write and not before it: encoding the
	// config below and dialing can take longer than any fixed delay on a slow
	// machine. A deadline cannot wait for the server, so that case is
	// repeated with a longer one until the write was reached.
	cancelAfterUpgrade := func(upgraded <-chan struct{}, cancel func()) {
		go func() {
			<-upgraded
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()
	}
	tests := []struct {
		name     string
		ctx      func(upgraded <-chan struct{}, d time.Duration) (context.Context, func())
		attempts []time.Duration
		want     []error
	}{
		{
			name: "canceled",
			ctx: func(upgraded <-chan struct{}, _ time.Duration) (context.Context, func()) {
				ctx, cancel := context.WithCancel(context.Background())
				cancelAfterUpgrade(upgraded, cancel)
				return ctx, cancel
			},
			attempts: []time.Duration{0},
			want:     []error{context.Canceled},
		},
		{
			name: "canceled with a cause",
			ctx: func(upgraded <-chan struct{}, _ time.Duration) (context.Context, func()) {
				ctx, cancel := context.WithCancelCause(context.Background())
				cancelAfterUpgrade(upgraded, func() { cancel(errMicrophone) })
				return ctx, func() { cancel(nil) }
			},
			attempts: []time.Duration{0},
			want:     []error{context.Canceled, errMicrophone},
		},
		{
			name: "deadline exceeded",
			ctx: func(_ <-chan struct{}, d time.Duration) (context.Context, func()) {
				return context.WithTimeout(context.Background(), d)
			},
			attempts: []time.Duration{300 * time.Millisecond, time.Second, 4 * time.Second},
			want:     []error{context.DeadlineExceeded},
		},
	}
	// Far more than the socket buffers of both ends hold, so the write of
	// the config blocks on a server that does not read.
	params := &ASRRealtimeParams{Context: &ASRContext{Terms: []string{strings.Repeat("x", 64<<20)}}}

	type result struct {
		stream *ASRRealtimeStream
		err    error
	}
	// attempt reports whether ctx ended after the upgrade, which is the case
	// under test.
	attempt := func(t *testing.T, newCtx func(<-chan struct{}, time.Duration) (context.Context, func()), d time.Duration, want []error) bool {
		upgraded := make(chan struct{})
		returned := make(chan struct{})
		closedByClient := make(chan bool, 1)
		f := newFakeASRServer(t, func(c *fakeASRConn) {
			close(upgraded)
			<-returned // reads nothing until the client gave up
			closedByClient <- c.waitClosed(5 * time.Second)
		})
		defer f.Close()

		ctx, cancel := newCtx(upgraded, d)
		defer cancel()
		finished := make(chan result, 1)
		go func() {
			stream, err := f.client().ASR.StreamWebSocket(ctx, make(chan []byte), params, nil)
			finished <- result{stream, err}
		}()
		var r result
		select {
		case r = <-finished:
		case <-ctx.Done():
			ended := time.Now()
			select {
			case r = <-finished:
				t.Logf("returned %v after ctx ended", time.Since(ended))
			case <-time.After(asrTestWait):
				close(returned)
				t.Fatal("StreamWebSocket is still blocked after ctx ended")
			}
		}
		close(returned)

		if r.err == nil {
			_ = r.stream.Close()
			t.Fatal("StreamWebSocket() error = nil")
		}
		for _, w := range want {
			if !errors.Is(r.err, w) {
				t.Errorf("error = %v, want errors.Is(err, %v)", r.err, w)
			}
		}
		select {
		case <-upgraded:
		default:
			return false // ctx ended before the connection was up
		}
		select {
		case closed := <-closedByClient:
			if !closed {
				t.Error("the client left the connection open")
			}
		case <-time.After(asrTestWait):
			t.Error("the server never saw the end of the connection")
		}
		assertNoASRGoroutines(t)
		return true
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, d := range tt.attempts {
				if attempt(t, tt.ctx, d, tt.want) {
					return
				}
				t.Logf("ctx ended before the config write (deadline %v)", d)
			}
			t.Fatal("ctx always ended before the config write was reached")
		})
	}
}

// The same for the upgrade itself: a server that accepts the connection and
// never answers.
func TestASRRealtime_CancelDuringHandshake(t *testing.T) {
	asrTestDeadline(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	closedByClient := make(chan bool, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Read the request and never answer; EOF means the client hung up.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 4096)
		for {
			if _, err := conn.Read(buf); err != nil {
				netErr, isNetErr := err.(net.Error)
				closedByClient <- !isNetErr || !netErr.Timeout()
				return
			}
		}
	}()

	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if want == context.Canceled {
			ctx, cancel = context.WithCancel(context.Background())
			time.AfterFunc(100*time.Millisecond, cancel)
		}
		client := NewClient(WithAPIKey("k"), WithBaseURL("http://"+listener.Addr().String()))
		var err error
		within(t, "StreamWebSocket during a stalled handshake", func() {
			_, err = client.ASR.StreamWebSocket(ctx, make(chan []byte), nil, nil)
		})
		cancel()
		if !errors.Is(err, want) {
			t.Errorf("error = %T (%v), want errors.Is(err, %v)", err, err, want)
		}
		// The goroutine above serves the first connection only. The second
		// one stays in the listener's backlog, which stalls the upgrade just
		// the same.
		if want == context.Canceled {
			select {
			case closed := <-closedByClient:
				if !closed {
					t.Error("the client left the connection open")
				}
			case <-time.After(asrTestWait):
				t.Error("the server never saw the end of the connection")
			}
		}
	}
	assertNoASRGoroutines(t)
}

// lateContext is a ctx whose deadline has passed but which is not done yet:
// what a caller's ctx looks like in the instant between its deadline and its
// timer firing. Deadline is the embedded ctx's own; Done and Err stay open.
type lateContext struct {
	context.Context
	open context.Context
}

func (c lateContext) Done() <-chan struct{} { return c.open.Done() }
func (c lateContext) Err() error            { return c.open.Err() }

// The dialer puts ctx's deadline on the connection, so a stalled upgrade can
// fail with the connection's own "i/o timeout" before ctx reports that it is
// done. Which of the two comes first is a race; the caller must get an error
// matching context.DeadlineExceeded either way. lateContext forces the side
// of the race where the connection is first.
func TestASRRealtime_DeadlineReportedByTheConnection(t *testing.T) {
	asrTestDeadline(t)
	shortSettle(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }() // accepts in its backlog, never answers

	withDeadline, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ctx := lateContext{Context: withDeadline, open: context.Background()}

	client := NewClient(WithAPIKey("k"), WithBaseURL("http://"+listener.Addr().String()))
	within(t, "StreamWebSocket during a stalled handshake", func() {
		_, err = client.ASR.StreamWebSocket(ctx, make(chan []byte), nil, nil)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %T (%v), want errors.Is(err, context.DeadlineExceeded)", err, err)
	}
	var wsErr *WebSocketError
	if !errors.As(err, &wsErr) {
		t.Errorf("error = %T, want a *WebSocketError", err)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("error = %v, want the connection's timeout to stay reachable", err)
	}
	assertNoASRGoroutines(t)
}

// notDueDone returns a ctx that has reached its deadline.
func notDueDone() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	return ctx
}

// earlyDeadlineContext reports a deadline earlier than the moment it is done:
// a ctx whose timer fires after the connection's, by a margin that does not
// depend on scheduling. Everything but Deadline is the embedded ctx's.
type earlyDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c earlyDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

// The connection times out first, and ctx has a cause of its own
// (context.WithTimeoutCause): the cause is only known once ctx is done, so
// the error must wait for that and carry it, not settle for
// context.DeadlineExceeded alone.
func TestASRRealtime_DeadlineReportedByTheConnectionKeepsCause(t *testing.T) {
	asrTestDeadline(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }() // accepts in its backlog, never answers

	errBudget := errors.New("transcription budget used up")
	done, cancel := context.WithTimeoutCause(context.Background(), 300*time.Millisecond, errBudget)
	defer cancel()
	ctx := earlyDeadlineContext{Context: done, deadline: time.Now().Add(50 * time.Millisecond)}

	client := NewClient(WithAPIKey("k"), WithBaseURL("http://"+listener.Addr().String()))
	within(t, "StreamWebSocket during a stalled handshake", func() {
		_, err = client.ASR.StreamWebSocket(ctx, make(chan []byte), nil, nil)
	})
	for _, want := range []error{context.DeadlineExceeded, errBudget} {
		if !errors.Is(err, want) {
			t.Errorf("error = %v, want errors.Is(err, %v)", err, want)
		}
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("error = %v, want the connection's timeout to stay reachable", err)
	}
	assertNoASRGoroutines(t)
}

// A timeout that is not ctx's is not reported as ctx's.
func TestASRStartupError(t *testing.T) {
	timeout := &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}
	notDue, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	for name, ctx := range map[string]context.Context{"no deadline": context.Background(), "deadline not reached": notDue} {
		if err := asrStartupError(ctx, timeout); err != error(timeout) {
			t.Errorf("%s: error = %v, want it unchanged", name, err)
		}
	}
	past, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	refused := errors.New("connection refused")
	if err := asrStartupError(lateContext{Context: past, open: context.Background()}, refused); err != refused {
		t.Errorf("not a timeout: error = %v, want it unchanged", err)
	}
	errMicrophone := errors.New("microphone unplugged")
	failed, fail := context.WithCancelCause(context.Background())
	fail(errMicrophone)
	err := asrStartupError(failed, refused)
	for _, want := range []error{context.Canceled, errMicrophone, refused} {
		if !errors.Is(err, want) {
			t.Errorf("canceled with a cause: error = %v, want errors.Is(err, %v)", err, want)
		}
	}
	err = asrStartupError(failed, context.Canceled)
	for _, want := range []error{context.Canceled, errMicrophone} {
		if !errors.Is(err, want) {
			t.Errorf("dialer returned ctx.Err(): error = %v, want errors.Is(err, %v)", err, want)
		}
	}
	if err := asrStartupError(notDueDone(), context.DeadlineExceeded); err != context.DeadlineExceeded {
		t.Errorf("error = %v, want context.DeadlineExceeded as it is", err)
	}
}

// Canceling ctx ends the session even while a send is stuck on a server that
// stopped reading: the writer cannot be the one that watches ctx.
func TestASRRealtime_CancelWhileSendIsBlocked(t *testing.T) {
	asrTestDeadline(t)
	errMicrophone := errors.New("microphone unplugged")
	release := make(chan struct{})
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() != nil {
			<-release // never reads again
		}
	})
	defer close(release)

	ctx, fail := context.WithCancelCause(context.Background())
	defer fail(nil)
	audio := make(chan []byte)
	stream, err := f.client().ASR.StreamWebSocket(ctx, audio, nil, nil)
	if err != nil {
		t.Fatalf("StreamWebSocket() error = %v", err)
	}
	if !stream.Next() {
		t.Fatalf("no session_started: %v", stream.Err())
	}

	stop := make(chan struct{})
	var pushed atomic.Int64
	go func() {
		chunk := make([]byte, 256<<10)
		for {
			select {
			case audio <- chunk:
				pushed.Add(1)
			case <-stop:
				return
			}
		}
	}()
	defer close(stop)

	// Wait until the socket is full and the sender is stuck in a write.
	deadline := time.Now().Add(10 * time.Second)
	for {
		before := pushed.Load()
		time.Sleep(200 * time.Millisecond)
		if pushed.Load() == before && before > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sender never stalled")
		}
	}

	// A control message queued behind the stuck write is released too.
	control := make(chan error, 1)
	go func() { control <- stream.Keepalive() }()
	time.Sleep(50 * time.Millisecond)

	fail(errMicrophone)
	within(t, "Next after ctx is canceled during a blocked send", func() {
		if stream.Next() {
			t.Error("Next() = true")
		}
	})
	if err := stream.Err(); !errors.Is(err, errMicrophone) {
		t.Errorf("Err() = %v, want the cancellation cause", err)
	}
	select {
	case err := <-control:
		if err == nil {
			t.Error("Keepalive() = nil for a message that was never sent")
		}
	case <-time.After(asrTestWait):
		t.Error("Keepalive() is still blocked after ctx was canceled")
	}
	assertNoASRGoroutines(t) // without Close
}

func TestASRRealtime_EarlyClose(t *testing.T) {
	asrTestDeadline(t)
	serverSawClose := make(chan bool, 1)
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		if c.start() == nil {
			return
		}
		c.send(msg{"type": "speech_start", "turn_index": 0, "audio_ms": 0})
		serverSawClose <- c.waitClosed(5 * time.Second)
	})

	audio := make(chan []byte) // still open: the caller leaves early
	stream := mustStream(t, f, context.Background(), audio, nil)
	for stream.Next() {
		if _, ok := stream.Event().(*ASRSpeechStartEvent); ok {
			break
		}
	}
	if err := stream.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
	if !<-serverSawClose {
		t.Error("server did not see the connection close")
	}
	assertNoASRGoroutines(t)
	if stream.Next() || stream.Err() != nil {
		t.Errorf("after Close: Next() = true or Err() = %v", stream.Err())
	}
	// The audio source is not left blocked forever by a session that is gone:
	// it is the caller's channel, and nothing reads it any more.
	select {
	case audio <- pcm(40):
		t.Error("the closed session still reads audio")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestASRRealtime_Ping(t *testing.T) {
	asrTestDeadline(t)
	var pings atomic.Int64
	f := newFakeASRServer(t, func(c *fakeASRConn) {
		c.conn.SetPingHandler(func(data string) error {
			pings.Add(1)
			return c.conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		})
		c.serve(nil)
	})

	run := func(interval time.Duration, want int64, wait time.Duration) int64 {
		pings.Store(0)
		opts := DefaultWebSocketOptions()
		opts.PingInterval = interval
		audio := make(chan []byte)
		stream, err := f.client().ASR.StreamWebSocket(context.Background(), audio, nil, opts)
		if err != nil {
			t.Fatalf("StreamWebSocket() error = %v", err)
		}
		// An idle audio source: wait for the pings rather than for a time
		// in which they should have come.
		deadline := time.Now().Add(wait)
		for pings.Load() < want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		close(audio)
		collect(t, stream)
		if err := stream.Err(); err != nil {
			t.Fatalf("Err() = %v", err)
		}
		assertNoASRGoroutines(t)
		return pings.Load()
	}
	if n := run(20*time.Millisecond, 3, asrTestWait); n < 3 {
		t.Errorf("server got %d pings at a 20 ms interval, want at least 3", n)
	}
	if n := run(0, 1, 200*time.Millisecond); n != 0 {
		t.Errorf("server got %d pings with PingInterval 0", n)
	}
}

func TestASRRealtimeError_Message(t *testing.T) {
	tests := []struct {
		err  ASRRealtimeError
		want string
	}{
		{ASRRealtimeError{Message: "boom", ErrorType: "internal", CloseCode: 4500}, "realtime ASR session failed (internal): boom [close code 4500]"},
		{ASRRealtimeError{Message: "idle timeout", CloseCode: 4408}, "realtime ASR session failed: idle timeout [close code 4408]"},
		{ASRRealtimeError{}, "realtime ASR session failed"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
	var _ FishAudioError = &ASRRealtimeError{}
}
