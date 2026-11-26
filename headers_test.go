package fishaudio

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

const (
	traceA = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceB = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
)

type traceKey struct{}

func traceFor(i int) string { return fmt.Sprintf("00-%032x-00f067aa0ba902b7-01", i) }

// fromContext is a provider that reads the trace from the call's ctx.
func fromContext(ctx context.Context) (http.Header, error) {
	tp, _ := ctx.Value(traceKey{}).(string)
	if tp == "" {
		return nil, nil
	}
	return http.Header{"Traceparent": {tp}}, nil
}

type recorded struct {
	key    string
	header http.Header
}

// headerServer answers every request with status, accepts WebSocket upgrades
// on /v1/tts/live (closing them at once), and records every request's headers.
func headerServer(t *testing.T, status int) (*httptest.Server, func() []recorded) {
	t.Helper()
	server, seen, _ := countingHeaderServer(t, status)
	return server, seen
}

// countingHeaderServer is headerServer that also counts the connections
// opened to it.
func countingHeaderServer(t *testing.T, status int) (*httptest.Server, func() []recorded, *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	var seen []recorded
	var conns atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, recorded{r.Method + " " + r.URL.Path, r.Header.Clone()})
		mu.Unlock()
		if r.URL.Path == "/v1/tts/live" {
			if conn, err := upgrader.Upgrade(w, r, nil); err == nil {
				_ = conn.Close()
			}
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
			return
		}
		switch r.URL.Path {
		case "/v1/tts":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte{1, 2})
		case "/model":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"items":[],"total":0}`))
				return
			}
			_, _ = w.Write([]byte(`{"_id":"v"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return server, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), seen...)
	}, &conns
}

func dialTTS(ctx context.Context, client *Client) error {
	textChan := make(chan string)
	close(textChan)
	stream, err := client.TTS.StreamWebSocket(ctx, textChan, &StreamParams{Model: ModelS2Pro}, nil)
	if err == nil {
		_ = stream.Close()
	}
	return err
}

func TestHeaderProvider_EveryPathOncePerRequestWithTheCallsContext(t *testing.T) {
	server, seen := headerServer(t, http.StatusOK)
	var calls atomic.Int32
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
		WithHeaderProvider(func(ctx context.Context) (http.Header, error) {
			calls.Add(1)
			return fromContext(ctx)
		}))
	ctx := context.WithValue(context.Background(), traceKey{}, traceA)

	// Errors from decoding the fake responses do not matter here; the
	// requests were sent either way.
	_, _ = client.Account.GetCredits(ctx, nil)
	_, _ = client.Account.GetPackage(ctx)
	_, _ = client.Voices.List(ctx, nil)
	_, _ = client.Voices.Get(ctx, "v1")
	_, _ = client.Voices.Create(ctx, &CreateVoiceParams{Title: "t", Voices: [][]byte{{0}}})
	_ = client.Voices.Update(ctx, "v1", &UpdateVoiceParams{Title: "t"})
	_ = client.Voices.Delete(ctx, "v1")
	_, _ = client.TTS.Convert(ctx, &ConvertParams{Text: "hi"})
	if stream, err := client.TTS.Stream(ctx, &StreamParams{Text: "hi"}); err == nil {
		_ = stream.Close()
	}
	_, _ = client.ASR.Transcribe(ctx, []byte{0, 1}, nil)
	_ = dialTTS(ctx, client)

	got := seen()
	if len(got) != 11 || int(calls.Load()) != 11 {
		t.Fatalf("%d requests, %d provider calls, want 11 each", len(got), calls.Load())
	}
	for _, r := range got {
		if v := r.header.Values("Traceparent"); len(v) != 1 || v[0] != traceA {
			t.Errorf("%s: traceparent %q, want [%q]", r.key, v, traceA)
		}
		if v := r.header.Values("Authorization"); len(v) != 1 || v[0] != "Bearer test-key" {
			t.Errorf("%s: Authorization %q", r.key, v)
		}
	}
}

func TestHeaderProvider_CalledOnceForAFailingRequest(t *testing.T) {
	server, seen := headerServer(t, http.StatusInternalServerError)
	var calls atomic.Int32
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
		WithHeaderProvider(func(ctx context.Context) (http.Header, error) {
			calls.Add(1)
			return http.Header{"Traceparent": {traceA}}, nil
		}))

	if _, err := client.TTS.Convert(context.Background(), &ConvertParams{Text: "hi"}); err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
	if calls.Load() != 1 || len(seen()) != 1 {
		t.Errorf("%d calls, %d requests, want 1 each", calls.Load(), len(seen()))
	}
}

func TestHeaderProvider_ConcurrentCallsEachSendTheirOwnContext(t *testing.T) {
	server, seen := headerServer(t, http.StatusOK)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL), WithHeaderProvider(fromContext))

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.WithValue(context.Background(), traceKey{}, traceFor(i))
			ctx = ContextWithHeaders(ctx, http.Header{"X-Call": {fmt.Sprint(i)}})
			if i%8 == 0 {
				if err := dialTTS(ctx, client); err != nil {
					t.Error(err)
				}
				return
			}
			if _, err := client.Account.GetCredits(ctx, nil); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	got := seen()
	if len(got) != 64 {
		t.Fatalf("%d requests, want 64", len(got))
	}
	for _, r := range got {
		var i int
		_, _ = fmt.Sscan(r.header.Get("X-Call"), &i)
		if v := r.header.Values("Traceparent"); len(v) != 1 || v[0] != traceFor(i) {
			t.Errorf("%s call %d: traceparent %q, want [%q]", r.key, i, v, traceFor(i))
		}
	}
}

func TestHeaderProvider_PrecedenceAndSDKHeaders(t *testing.T) {
	server, seen := headerServer(t, http.StatusOK)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
		WithHeaderProvider(func(ctx context.Context) (http.Header, error) {
			return http.Header{
				"traceparent":       {traceA},
				"Baggage":           {"k=v"},
				"authorization":     {"Bearer stolen"},
				"Authorization":     {"Bearer stolen"},
				"Model":             {"s1"},
				"User-Agent":        {"evil/1"},
				"Content-Type":      {"text/plain"},
				"Content-Length":    {"1"},
				"Host":              {"example.com"},
				"Connection":        {"close"},
				"Sec-WebSocket-Key": {"x"},
				"Upgrade":           {"h2c"},
			}, nil
		}))
	ctx := ContextWithHeaders(context.Background(), http.Header{"traceparent": {"ignored"}})
	ctx = ContextWithHeaders(ctx, http.Header{
		"Traceparent":   {traceB},
		"Authorization": {"Bearer per-call"},
		"User-Agent":    {"my-app/1.0"},
	})

	_, _ = client.TTS.Convert(ctx, &ConvertParams{Text: "hi", Model: ModelS2Pro})
	_ = dialTTS(ctx, client)

	for _, r := range seen() {
		h := r.header
		check := func(name string, want ...string) {
			t.Helper()
			if got := h.Values(name); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s: %s = %q, want %q", r.key, name, got, want)
			}
		}
		check("Traceparent", traceB)
		check("Baggage", "k=v")
		check("Authorization", "Bearer test-key")
		check("Model", "s2-pro")
		check("User-Agent", userAgent+" my-app/1.0")
		if h.Get("Content-Type") == "text/plain" || h.Get("Content-Length") == "1" {
			t.Errorf("%s: provider set Content-Type or Content-Length", r.key)
		}
		if r.key == "GET /v1/tts/live" && (h.Get("Sec-Websocket-Key") == "x" || h.Get("Upgrade") != "websocket") {
			t.Errorf("%s: provider changed the handshake", r.key)
		}
	}
	if len(seen()) != 2 {
		t.Fatalf("%d requests, want 2", len(seen()))
	}
}

func TestHeaderProvider_ReturningNothingAddsNothing(t *testing.T) {
	for name, result := range map[string]http.Header{
		"nil":          nil,
		"empty":        {},
		"empty values": {"Traceparent": {}},
	} {
		t.Run(name, func(t *testing.T) {
			server, seen := headerServer(t, http.StatusOK)
			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
				WithHeaderProvider(func(context.Context) (http.Header, error) { return result, nil }))
			_, _ = client.Account.GetCredits(context.Background(), nil)
			_ = dialTTS(context.Background(), client)
			for _, r := range seen() {
				if _, ok := r.header["Traceparent"]; ok {
					t.Errorf("%s: traceparent sent", r.key)
				}
			}
		})
	}
}

func TestHeaderProvider_InvalidHeadersFailBeforeSending(t *testing.T) {
	for name, result := range map[string]http.Header{
		"line break in value": {"Traceparent": {"a\r\nInjected: yes"}},
		"NUL in value":        {"Traceparent": {"a\x00b"}},
		"space in name":       {"Bad Name": {"x"}},
		"empty name":          {"": {"x"}},
	} {
		t.Run(name, func(t *testing.T) {
			server, seen := headerServer(t, http.StatusOK)
			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
				WithHeaderProvider(func(context.Context) (http.Header, error) { return result, nil }))
			ctx := context.Background()
			errs := []error{dialTTS(ctx, client)}
			_, err := client.Account.GetCredits(ctx, nil)
			errs = append(errs, err)
			_, err = client.ASR.Transcribe(ctx, []byte{0}, nil)
			errs = append(errs, err)
			_, err = client.Voices.Create(ctx, &CreateVoiceParams{Title: "t", Voices: [][]byte{{0}}})
			errs = append(errs, err)
			for _, err := range errs {
				if err == nil || !strings.Contains(err.Error(), "header provider") || strings.Contains(err.Error(), "Injected") {
					t.Errorf("err = %v", err)
				}
			}
			if n := len(seen()); n != 0 {
				t.Errorf("%d requests sent", n)
			}
		})
	}

	server, seen := headerServer(t, http.StatusOK)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	ctx := ContextWithHeaders(context.Background(), http.Header{"X-Id": {"a\nb"}})
	if _, err := client.Account.GetCredits(ctx, nil); err == nil || !strings.Contains(err.Error(), "ContextWithHeaders") {
		t.Errorf("per-call: err = %v", err)
	}
	if len(seen()) != 0 {
		t.Error("per-call: request sent")
	}
}

var errTracerDown = errors.New("tracer is down")

type providerError struct{ reason string }

func (e *providerError) Error() string { return "provider: " + e.reason }

func TestHeaderProvider_ErrorFailsEveryPathBeforeSending(t *testing.T) {
	for name, providerErr := range map[string]error{
		"sentinel":     errTracerDown,
		"wrapped":      fmt.Errorf("span lookup: %w", errTracerDown),
		"custom error": &providerError{"no exporter"},
	} {
		t.Run(name, func(t *testing.T) {
			server, seen := headerServer(t, http.StatusOK)
			var calls atomic.Int32
			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
				WithHeaderProvider(func(context.Context) (http.Header, error) {
					calls.Add(1)
					// The headers must not be sent alongside the error.
					return http.Header{"Traceparent": {traceA}}, providerErr
				}))
			// Per-call headers are set, and must not mask the provider's error.
			ctx := ContextWithHeaders(context.Background(), http.Header{"Traceparent": {traceB}})

			errs := map[string]error{}
			_, errs["Account.GetCredits"] = client.Account.GetCredits(ctx, nil)
			_, errs["Account.GetPackage"] = client.Account.GetPackage(ctx)
			_, errs["Voices.List"] = client.Voices.List(ctx, nil)
			_, errs["Voices.Get"] = client.Voices.Get(ctx, "v1")
			_, errs["Voices.Create"] = client.Voices.Create(ctx, &CreateVoiceParams{Title: "t", Voices: [][]byte{{0}}})
			errs["Voices.Update"] = client.Voices.Update(ctx, "v1", &UpdateVoiceParams{Title: "t"})
			errs["Voices.Delete"] = client.Voices.Delete(ctx, "v1")
			_, errs["TTS.Convert"] = client.TTS.Convert(ctx, &ConvertParams{Text: "hi"})
			stream, err := client.TTS.Stream(ctx, &StreamParams{Text: "hi"})
			if stream != nil {
				_ = stream.Close()
			}
			errs["TTS.Stream"] = err
			_, errs["ASR.Transcribe"] = client.ASR.Transcribe(ctx, []byte{0, 1}, nil)
			errs["TTS.StreamWebSocket"] = dialTTS(ctx, client)

			for call, err := range errs {
				if err == nil {
					t.Errorf("%s: no error", call)
					continue
				}
				if !errors.Is(err, providerErr) {
					t.Errorf("%s: errors.Is(%v, provider error) = false", call, err)
				}
				var custom *providerError
				if _, isCustom := providerErr.(*providerError); isCustom != errors.As(err, &custom) {
					t.Errorf("%s: errors.As(%v, *providerError) = %v", call, err, !isCustom)
				}
				if want := "fishaudio: header provider: " + providerErr.Error(); err.Error() != want {
					t.Errorf("%s: err = %q, want %q", call, err, want)
				}
			}
			if n := int(calls.Load()); n != len(errs) {
				t.Errorf("%d provider calls, want %d", n, len(errs))
			}
			for _, r := range seen() {
				t.Errorf("%s reached the server", r.key)
			}
		})
	}
}

func TestHeaderProvider_PanicIsNotRecoveredAndNothingIsSent(t *testing.T) {
	server, seen := headerServer(t, http.StatusOK)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
		WithHeaderProvider(func(context.Context) (http.Header, error) { panic("tracer is down") }))
	for _, call := range []func(){
		func() { _, _ = client.TTS.Convert(context.Background(), &ConvertParams{Text: "hi"}) },
		func() { _ = dialTTS(context.Background(), client) },
	} {
		func() {
			defer func() {
				if r := recover(); r != "tracer is down" {
					t.Errorf("recovered %v", r)
				}
			}()
			call()
		}()
	}
	if len(seen()) != 0 {
		t.Error("a request was sent")
	}
}

func TestContextWithHeaders_MergesAndCopies(t *testing.T) {
	h := http.Header{"x-a": {"1"}}
	ctx := ContextWithHeaders(context.Background(), h)
	h.Set("X-A", "changed")
	ctx = ContextWithHeaders(ctx, http.Header{"X-B": {"2"}})
	ctx2 := ContextWithHeaders(ctx, http.Header{"X-A": {"3"}})

	got := ctx.Value(requestHeadersKey{}).(http.Header)
	if got.Get("X-A") != "1" || got.Get("X-B") != "2" {
		t.Errorf("ctx headers = %v", got)
	}
	if got2 := ctx2.Value(requestHeadersKey{}).(http.Header); got2.Get("X-A") != "3" || got2.Get("X-B") != "2" {
		t.Errorf("ctx2 headers = %v", got2)
	}
}

func TestHeaderProvider_WithoutOneNothingChanges(t *testing.T) {
	server, seen := headerServer(t, http.StatusOK)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	_, _ = client.Account.GetCredits(context.Background(), nil)
	_ = dialTTS(context.Background(), client)
	for _, r := range seen() {
		if _, ok := r.header["Traceparent"]; ok {
			t.Errorf("%s: traceparent sent", r.key)
		}
	}
}

// inPlaceAppender is a transport that appends to the request's own value
// slices instead of copying them, as a careless transport might. If the SDK
// put the provider's slices on the request, this would write into the
// provider's header.
type inPlaceAppender struct{ next http.RoundTripper }

func (a inPlaceAppender) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header["X-Shared"] = append(req.Header["X-Shared"], "added")
	return a.next.RoundTrip(req)
}

// TestHeaderProvider_ContractFollowingProvidersAreRaceFree runs, under -race,
// the two providers the documentation allows: one that builds a new header on
// every call, and one that returns the same header every time and never
// modifies it. Neither may race with the SDK, and the SDK must never write
// into the shared one.
func TestHeaderProvider_ContractFollowingProvidersAreRaceFree(t *testing.T) {
	shared := http.Header{"X-Shared": make([]string, 1, 8)}
	shared["X-Shared"][0] = "v"
	providers := map[string]HeaderProvider{
		"new header per call": fromContext,
		"shared, never modified": func(context.Context) (http.Header, error) {
			return shared, nil
		},
	}
	for name, provider := range providers {
		t.Run(name, func(t *testing.T) {
			server, seen := headerServer(t, http.StatusOK)
			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL), WithHeaderProvider(provider),
				WithHTTPClient(&http.Client{Transport: inPlaceAppender{server.Client().Transport}}))
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					ctx := context.WithValue(context.Background(), traceKey{}, traceFor(i))
					var err error
					if i%4 == 0 {
						err = dialTTS(ctx, client)
					} else {
						_, err = client.Account.GetCredits(ctx, nil)
					}
					if err != nil {
						t.Error(err)
					}
				}(i)
			}
			wg.Wait()
			if n := len(seen()); n != 32 {
				t.Errorf("%d requests, want 32", n)
			}
		})
	}
	if got := shared["X-Shared"]; len(got) != 1 || got[0] != "v" || got[:2][1] != "" {
		t.Errorf("the SDK wrote into the provider's header: %q (backing %q)", got, got[:2])
	}
}

func TestContextWithHeaders_NameWithNoValuesKeepsTheProvidersOff(t *testing.T) {
	logs := captureTraceWarnings(t)
	server := traceGateway(t, newTrace, http.StatusOK)
	var seen []http.Header
	var mu sync.Mutex
	record := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		seen = append(seen, req.Header.Clone())
		mu.Unlock()
		return http.DefaultTransport.RoundTrip(req)
	})
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL), WithHTTPClient(&http.Client{Transport: record}),
		WithHeaderProvider(func(context.Context) (http.Header, error) {
			return http.Header{"Traceparent": {traceA}, "X-Other": {"kept"}, "User-Agent": {"evil/1"}}, nil
		}))
	layered := ContextWithHeaders(context.Background(), http.Header{"Traceparent": {traceB}})
	for name, ctx := range map[string]context.Context{
		"nil":                     ContextWithHeaders(context.Background(), http.Header{"Traceparent": nil}),
		"empty slice":             ContextWithHeaders(context.Background(), http.Header{"traceparent": {}}),
		"empty over an earlier":   ContextWithHeaders(layered, http.Header{"Traceparent": nil}),
		"empty User-Agent":        ContextWithHeaders(context.Background(), http.Header{"Traceparent": nil, "User-Agent": nil}),
		"empty reserved name too": ContextWithHeaders(context.Background(), http.Header{"Traceparent": {}, "Authorization": nil}),
	} {
		mu.Lock()
		seen = nil
		mu.Unlock()
		if _, err := client.Account.GetCredits(ctx, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mu.Lock()
		got := seen
		mu.Unlock()
		if len(got) != 1 {
			t.Fatalf("%s: %d requests", name, len(got))
		}
		h := got[0]
		if _, ok := h["Traceparent"]; ok {
			t.Errorf("%s: traceparent %q sent, want none", name, h["Traceparent"])
		}
		if h.Get("X-Other") != "kept" || h.Get("User-Agent") != userAgent || h.Get("Authorization") != "Bearer test-key" {
			t.Errorf("%s: headers %q", name, h)
		}
	}
	// The WebSocket handshake too.
	wsServer, wsSeen := headerServer(t, http.StatusOK)
	wsClient := NewClient(WithAPIKey("test-key"), WithBaseURL(wsServer.URL), WithHeaderProvider(sendTrace(traceA)))
	if err := dialTTS(ContextWithHeaders(context.Background(), http.Header{"Traceparent": nil}), wsClient); err != nil {
		t.Fatal(err)
	}
	if got := wsSeen(); len(got) != 1 || got[0].header["Traceparent"] != nil {
		t.Errorf("WebSocket: %v", got)
	}
	// Nothing was sent, so nothing is compared.
	if got := logs.take(); len(got) != 0 {
		t.Errorf("logged %v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHeaderNames_CaseVariantsCombineInByteOrderOfTheSpelling(t *testing.T) {
	mixed := http.Header{
		"traceparent": {traceA},
		"Traceparent": {traceB},
		"x-id":        {"2"},
		"X-Id":        {"1"},
		"X-ID":        {"0", "0b"},
	}
	// Byte order of the spellings: "Traceparent" < "traceparent" and
	// "X-ID" < "X-Id" < "x-id".
	wantTrace := []string{traceB, traceA}
	wantID := []string{"0", "0b", "1", "2"}
	for _, source := range headerSources {
		for _, path := range headerPaths {
			opts := []ClientOption{WithAPIKey("k")}
			ctx := context.Background()
			if source == providerSource {
				opts = append(opts, WithHeaderProvider(func(context.Context) (http.Header, error) { return mixed, nil }))
			} else {
				ctx = ContextWithHeaders(ctx, mixed)
			}
			call := func(client *Client) error {
				if path == "ws" {
					return dialTTS(ctx, client)
				}
				_, err := client.Account.GetCredits(ctx, nil)
				return err
			}

			server, seen := headerServer(t, http.StatusOK)
			client := NewClient(append(opts, WithBaseURL(server.URL))...)
			for i := 0; i < 100; i++ {
				if err := call(client); err != nil {
					t.Fatal(err)
				}
			}
			for i, r := range seen() {
				if fmt.Sprint(r.header["Traceparent"]) != fmt.Sprint(wantTrace) || fmt.Sprint(r.header["X-Id"]) != fmt.Sprint(wantID) {
					t.Fatalf("%s/%s call %d: traceparent %q, X-Id %q; want %q, %q",
						source, path, i, r.header["Traceparent"], r.header["X-Id"], wantTrace, wantID)
				}
			}

			// Two traceparent values: the existing warning reports them.
			logs := captureTraceWarnings(t)
			gateway := traceGateway(t, continueTrace, http.StatusOK)
			if err := call(NewClient(append(opts, WithBaseURL(gateway.URL))...)); err != nil {
				t.Fatal(err)
			}
			recs := logs.take()
			if len(recs) != 1 || recs[0].attrs["sent_trace_id"] != "(2 traceparent values)" ||
				!strings.Contains(recs[0].attrs["hint"], "2 traceparent headers") {
				t.Errorf("%s/%s: logged %v", source, path, recs)
			}
		}
	}
}

func TestHeaderNames_ErrorDoesNotDependOnMapOrder(t *testing.T) {
	bad := http.Header{"X-B": {"\x01"}, "X-A": {"é"}, "X-C": {" c"}}
	for _, source := range headerSources {
		opts := []ClientOption{WithAPIKey("k")}
		ctx := context.Background()
		if source == providerSource {
			opts = append(opts, WithHeaderProvider(func(context.Context) (http.Header, error) { return bad, nil }))
		} else {
			ctx = ContextWithHeaders(ctx, bad)
		}
		client := NewClient(opts...)
		want := "fishaudio: " + source + `: value of header "X-A" has a non-ASCII character`
		for i := 0; i < 200; i++ {
			if _, err := client.addHeaders(ctx, http.Header{}); err == nil || err.Error() != want {
				t.Fatalf("%s run %d: err = %v, want %q", source, i, err, want)
			}
		}
	}
}
