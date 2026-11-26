package fishaudio

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// traceMode is how traceGateway answers.
type traceMode int

const (
	// continueTrace answers like the Fish Audio gateway: the trace id of
	// exactly one valid traceparent, otherwise a new one.
	continueTrace traceMode = iota
	// newTrace always answers with a new trace id.
	newTrace
	// noTraceID sends no X-Fish-Trace-Id, like another base URL or a proxy.
	noTraceID
)

const freshTraceID = "11111111111111111111111111111111"

// traceGateway serves every SDK path, WebSocket included, answering with
// status and an X-Fish-Trace-Id chosen by mode.
func traceGateway(t *testing.T, mode traceMode, status int) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := http.Header{}
		switch mode {
		case continueTrace:
			id := freshTraceID
			if values := r.Header.Values("Traceparent"); len(values) == 1 {
				if sent, ok := traceIDOf(values[0]); ok {
					id = sent
				}
			}
			header.Set("X-Fish-Trace-Id", id)
		case newTrace:
			header.Set("X-Fish-Trace-Id", freshTraceID)
		}
		if r.URL.Path == "/v1/tts/live" && status == http.StatusOK {
			if conn, err := upgrader.Upgrade(w, r, header); err == nil {
				_ = conn.Close()
			}
			return
		}
		for name, values := range header {
			w.Header()[name] = values
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
			return
		}
		switch {
		case r.URL.Path == "/v1/tts":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte{1, 2})
		case r.URL.Path == "/model" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		case r.URL.Path == "/model":
			_, _ = w.Write([]byte(`{"_id":"v"}`))
		case r.URL.Path == "/v1/asr":
			_, _ = w.Write([]byte(`{"text":"hi","duration":1}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// tracePaths are the SDK's 11 request paths and the TTS WebSocket handshake.
var tracePaths = []struct {
	name string
	call func(ctx context.Context, c *Client) error
}{
	{"Account.GetCredits", func(ctx context.Context, c *Client) error { _, err := c.Account.GetCredits(ctx, nil); return err }},
	{"Account.GetPackage", func(ctx context.Context, c *Client) error { _, err := c.Account.GetPackage(ctx); return err }},
	{"Voices.List", func(ctx context.Context, c *Client) error { _, err := c.Voices.List(ctx, nil); return err }},
	{"Voices.Get", func(ctx context.Context, c *Client) error { _, err := c.Voices.Get(ctx, "v1"); return err }},
	{"Voices.Create", func(ctx context.Context, c *Client) error {
		_, err := c.Voices.Create(ctx, &CreateVoiceParams{Title: "t", Voices: [][]byte{{0}}})
		return err
	}},
	{"Voices.Update", func(ctx context.Context, c *Client) error {
		return c.Voices.Update(ctx, "v1", &UpdateVoiceParams{Title: "t"})
	}},
	{"Voices.Delete", func(ctx context.Context, c *Client) error { return c.Voices.Delete(ctx, "v1") }},
	{"TTS.Convert", func(ctx context.Context, c *Client) error {
		_, err := c.TTS.Convert(ctx, &ConvertParams{Text: "hi"})
		return err
	}},
	{"TTS.Stream", func(ctx context.Context, c *Client) error {
		stream, err := c.TTS.Stream(ctx, &StreamParams{Text: "hi"})
		if err == nil {
			_ = stream.Close()
		}
		return err
	}},
	{"ASR.Transcribe", func(ctx context.Context, c *Client) error {
		_, err := c.ASR.Transcribe(ctx, []byte{0, 1}, nil)
		return err
	}},
	{"TTS.StreamWebSocket", dialTTS},
}

type logRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

type logCapture struct {
	mu      sync.Mutex
	records []logRecord
}

func (h *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *logCapture) Handle(_ context.Context, r slog.Record) error {
	rec := logRecord{level: r.Level, msg: r.Message, attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec)
	return nil
}
func (h *logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logCapture) WithGroup(string) slog.Handler      { return h }

func (h *logCapture) take() []logRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.records
	h.records = nil
	return out
}

// captureTraceWarnings routes slog.Default to a capture and lets the
// once-per-process warning fire again; both are restored afterwards.
func captureTraceWarnings(t *testing.T) *logCapture {
	t.Helper()
	capture := &logCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	traceWarned.Store(false)
	t.Cleanup(func() {
		slog.SetDefault(previous)
		traceWarned.Store(false)
	})
	return capture
}

func sendTrace(value ...string) HeaderProvider {
	return func(context.Context) (http.Header, error) {
		return http.Header{"Traceparent": value}, nil
	}
}

func TestTraceWarning_ContinuedTraceLogsNothing(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		logs := captureTraceWarnings(t)
		server := traceGateway(t, continueTrace, status)
		client := NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(fromContext))
		for i, path := range tracePaths {
			ctx := context.WithValue(context.Background(), traceKey{}, traceFor(i+1))
			_ = path.call(ctx, client)
			// Through ContextWithHeaders, with the provider sending nothing.
			_ = path.call(ContextWithHeaders(context.Background(), http.Header{"Traceparent": {traceA}}), client)
		}
		if got := logs.take(); len(got) != 0 {
			t.Errorf("status %d: logged %v, want nothing", status, got)
		}
	}
}

func TestTraceWarning_EveryPathWarnsWhenNotContinued(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		server := traceGateway(t, newTrace, status)
		client := NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(sendTrace(traceA)))
		for _, path := range tracePaths {
			logs := captureTraceWarnings(t)
			err := path.call(context.Background(), client)
			if status != http.StatusOK && err == nil {
				t.Errorf("%s: no error for status %d", path.name, status)
			}
			got := logs.take()
			if len(got) != 1 {
				t.Errorf("%s status %d: %d log records, want 1", path.name, status, len(got))
				continue
			}
			rec := got[0]
			if rec.level != slog.LevelWarn || rec.msg != "fishaudio: the API did not continue the trace this SDK sent" {
				t.Errorf("%s: %v %q", path.name, rec.level, rec.msg)
			}
			if rec.attrs["sent_trace_id"] != traceA[3:35] || rec.attrs["api_trace_id"] != freshTraceID {
				t.Errorf("%s: attrs %v", path.name, rec.attrs)
			}
			if !strings.Contains(rec.attrs["hint"], "otelhttp") ||
				!strings.Contains(rec.attrs["hint"], "https://docs.fish.audio/api-reference/observability") {
				t.Errorf("%s: hint %q", path.name, rec.attrs["hint"])
			}
		}
	}
}

func TestTraceWarning_OncePerProcessAcrossCallsAndGoroutines(t *testing.T) {
	logs := captureTraceWarnings(t)
	server := traceGateway(t, newTrace, http.StatusOK)
	clients := []*Client{
		NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(sendTrace(traceA))),
		NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(sendTrace("00-not-a-trace"))),
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := tracePaths[i%len(tracePaths)]
			for j := 0; j < 3; j++ {
				if err := path.call(context.Background(), clients[i%2]); err != nil {
					t.Errorf("%s: %v", path.name, err)
				}
			}
		}(i)
	}
	wg.Wait()
	if got := logs.take(); len(got) != 1 {
		t.Fatalf("%d log records, want exactly 1: %v", len(got), got)
	}
}

func TestTraceWarning_NoTraceIDInResponseLogsNothing(t *testing.T) {
	logs := captureTraceWarnings(t)
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		server := traceGateway(t, noTraceID, status)
		client := NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(sendTrace("00-not-a-trace")))
		for _, path := range tracePaths {
			_ = path.call(context.Background(), client)
		}
	}
	// Nothing answers at all.
	client := NewClient(WithAPIKey("k"), WithBaseURL("http://127.0.0.1:1"), WithHeaderProvider(sendTrace("00-not-a-trace")))
	for _, path := range tracePaths {
		if err := path.call(context.Background(), client); err == nil {
			t.Errorf("%s: no error without a server", path.name)
		}
	}
	if got := logs.take(); len(got) != 0 {
		t.Errorf("logged %v, want nothing", got)
	}
}

func TestTraceWarning_NoTraceparentFromTheSDKLogsNothing(t *testing.T) {
	logs := captureTraceWarnings(t)
	server := traceGateway(t, newTrace, http.StatusOK)

	// No provider and no ContextWithHeaders, or a provider sending nothing.
	for _, client := range []*Client{
		NewClient(WithAPIKey("k"), WithBaseURL(server.URL)),
		NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(fromContext)),
	} {
		for _, path := range tracePaths {
			_ = path.call(context.Background(), client)
		}
	}
	// A traceparent the caller set through RequestOptions is the caller's
	// own, not one the SDK put there.
	client := NewClient(WithAPIKey("k"), WithBaseURL(server.URL))
	opts := &RequestOptions{AdditionalHeaders: map[string]string{"traceparent": traceA}}
	resp, err := client.doRequest(context.Background(), http.MethodGet, "/wallet/self/api-credit", nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := logs.take(); len(got) != 0 {
		t.Errorf("logged %v, want nothing", got)
	}
}

func TestTraceWarning_InvalidSentValueIsNotLogged(t *testing.T) {
	const marker = "secret-marker"
	for _, sent := range [][]string{
		{"00-" + marker},
		{strings.ToUpper(traceA)},
		{traceA, traceB},
	} {
		logs := captureTraceWarnings(t)
		server := traceGateway(t, newTrace, http.StatusOK)
		client := NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(sendTrace(sent...)))
		audio, err := client.TTS.Convert(context.Background(), &ConvertParams{Text: "hi"})
		if err != nil || len(audio) != 2 {
			t.Fatalf("%q: Convert = %v, %v; the check changed the call", sent, audio, err)
		}
		got := logs.take()
		if len(got) != 1 {
			t.Fatalf("%q: %d log records, want 1", sent, len(got))
		}
		for key, value := range got[0].attrs {
			for _, v := range sent {
				if strings.Contains(value, v) || strings.Contains(value, marker) {
					t.Errorf("%q: attribute %s echoes the sent value: %q", sent, key, value)
				}
			}
		}
		if got[0].attrs["api_trace_id"] != freshTraceID || !strings.HasPrefix(got[0].attrs["sent_trace_id"], "(") {
			t.Errorf("%q: attrs %v", sent, got[0].attrs)
		}
		hint := got[0].attrs["hint"]
		if len(sent) > 1 && !strings.Contains(hint, "2 traceparent headers") ||
			len(sent) == 1 && !strings.Contains(hint, "not one the API accepts") {
			t.Errorf("%q: hint %q", sent, hint)
		}
	}
}

func TestTraceWarning_ComparesCaseInsensitively(t *testing.T) {
	logs := captureTraceWarnings(t)
	upper := strings.ToUpper(traceA[3:35])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Fish-Trace-Id", upper)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(WithAPIKey("k"), WithBaseURL(server.URL), WithHeaderProvider(sendTrace(traceA)))
	if _, err := client.Account.GetCredits(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := logs.take(); len(got) != 0 {
		t.Errorf("logged %v, want nothing", got)
	}
}

func TestTraceIDOf(t *testing.T) {
	for value, want := range map[string]string{
		traceA:                  traceA[3:35],
		" \t" + traceA + " ":    traceA[3:35],
		strings.ToUpper(traceA): "",
		traceA + "-extra":       "",
		"01" + traceA[2:]:       "",
		"ff" + traceA[2:]:       "",
		"00-" + strings.Repeat("0", 32) + traceA[35:]:       "",
		traceA[:36] + strings.Repeat("0", 16) + traceA[52:]: "",
		strings.Replace(traceA, "-", "_", 1):                "",
		traceA[:54] + "g":                                   "",
		"00-not-a-trace":                                    "",
		"":                                                  "",
	} {
		got, ok := traceIDOf(value)
		if got != want || ok != (want != "") {
			t.Errorf("traceIDOf(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}
}
