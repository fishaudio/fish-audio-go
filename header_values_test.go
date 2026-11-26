package fishaudio

import (
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// countingTransport counts the requests that reach the HTTP transport.
type countingTransport struct {
	trips atomic.Int32
	next  http.RoundTripper
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.trips.Add(1)
	return c.next.RoundTrip(req)
}

// valueProbe sends header values through the SDK to a local test server and
// reports what reached it.
type valueProbe struct {
	t         *testing.T
	baseURL   string
	seen      func() []recorded
	conns     *atomic.Int32
	transport *countingTransport
}

func newValueProbe(t *testing.T) *valueProbe {
	server, seen, conns := countingHeaderServer(t, http.StatusOK)
	return &valueProbe{t, server.URL, seen, conns, &countingTransport{next: server.Client().Transport}}
}

// send makes one call on path ("http" or "ws") with header set by source
// ("header provider" or "ContextWithHeaders"). It returns how many requests
// reached the HTTP transport or connections reached the server, the headers
// the server received (nil if none), and the call's error.
func (p *valueProbe) send(source, path string, header http.Header) (int, http.Header, error) {
	p.t.Helper()
	opts := []ClientOption{WithAPIKey("test-key"), WithBaseURL(p.baseURL),
		WithHTTPClient(&http.Client{Transport: p.transport})}
	ctx := context.Background()
	if source == providerSource {
		opts = append(opts, WithHeaderProvider(func(context.Context) (http.Header, error) {
			return header.Clone(), nil
		}))
	} else {
		ctx = ContextWithHeaders(ctx, header)
	}
	client := NewClient(opts...)

	trips, conns, seen := p.transport.trips.Load(), p.conns.Load(), len(p.seen())
	var err error
	if path == "ws" {
		err = dialTTS(ctx, client)
	} else {
		_, err = client.Account.GetCredits(ctx, nil)
	}
	sent := int(p.transport.trips.Load() - trips)
	if path == "ws" {
		sent = int(p.conns.Load() - conns)
	}
	got := p.seen()
	if len(got) == seen {
		return sent, nil, err
	}
	return sent, got[len(got)-1].header, err
}

var (
	headerSources = []string{providerSource, perCallSource}
	headerPaths   = []string{"http", "ws"}
)

// leaksValue reports whether msg contains value, raw or Go-quoted, or the
// escaped bytes of a value that has any.
func leaksValue(msg, value string) bool {
	if value == "" {
		return false
	}
	quoted := strconv.Quote(value)
	return strings.Contains(msg, value) || strings.Contains(msg, quoted[1:len(quoted)-1])
}

func TestHeaderValues_ExplicitCases(t *testing.T) {
	const (
		whitespace = "leading or trailing whitespace"
		nonASCII   = "a non-ASCII character"
		control    = "a control character"
	)
	cases := []struct {
		values  []string
		problem string // "" if the values are sent
	}{
		{[]string{"café"}, nonASCII},
		{[]string{"日本"}, nonASCII},
		{[]string{" a"}, whitespace},
		{[]string{"a "}, whitespace},
		{[]string{"\ta"}, whitespace},
		{[]string{"a\x7fb"}, control},
		{[]string{"a\x01b"}, control},
		{[]string{"a\x00b"}, control},
		{[]string{"a\r\nb"}, control},
		{[]string{"a\nb"}, control},
		{[]string{""}, ""},
		{[]string{"a b"}, ""},
		{[]string{"a\tb"}, ""},
		{[]string{"ok", "a\x01b"}, control},
		{[]string{"ok", "also ok"}, ""},
	}
	p := newValueProbe(t)
	for _, tc := range cases {
		for _, source := range headerSources {
			for _, path := range headerPaths {
				name := fmt.Sprintf("%q/%s/%s", tc.values, source, path)
				sent, got, err := p.send(source, path, http.Header{"X-Probe": tc.values})
				if tc.problem == "" {
					if err != nil || sent != 1 {
						t.Errorf("%s: err = %v, %d sent", name, err, sent)
					} else if fmt.Sprintf("%q", got["X-Probe"]) != fmt.Sprintf("%q", tc.values) {
						t.Errorf("%s: server got %q", name, got["X-Probe"])
					}
					continue
				}
				want := fmt.Sprintf("fishaudio: %s: value of header \"X-Probe\" has %s", source, tc.problem)
				if err == nil || err.Error() != want {
					t.Errorf("%s: err = %v, want %q", name, err, want)
				}
				if sent != 0 || got != nil {
					t.Errorf("%s: %d sent, server got %q", name, sent, got)
				}
				for _, v := range tc.values {
					if err != nil && v != "ok" && leaksValue(err.Error(), v) {
						t.Errorf("%s: error %q shows the value", name, err)
					}
				}
			}
		}
	}
}

// TestHeaderValues_EveryByteAtEveryPosition checks that the SDK and the
// transports agree for every byte: a value the SDK accepts reaches the
// server unchanged on both paths, and a value it rejects fails with the
// SDK's own error, without the value, before anything is sent.
func TestHeaderValues_EveryByteAtEveryPosition(t *testing.T) {
	p := newValueProbe(t)
	for b := 0; b < 256; b++ {
		visible := b >= 0x21 && b <= 0x7e
		for _, pos := range []string{"start", "middle", "end"} {
			var value string
			switch c := string([]byte{byte(b)}); pos {
			case "start":
				value = c + "ab"
			case "middle":
				value = "a" + c + "b"
			case "end":
				value = "ab" + c
			}
			accept := visible || pos == "middle" && (b == ' ' || b == '\t')
			for _, source := range headerSources {
				for _, path := range headerPaths {
					name := fmt.Sprintf("0x%02x/%s/%s/%s", b, pos, source, path)
					sent, got, err := p.send(source, path, http.Header{"X-Probe": {value}})
					if accept {
						if err != nil || sent != 1 {
							t.Errorf("%s: err = %v, %d sent", name, err, sent)
						} else if v := got["X-Probe"]; len(v) != 1 || v[0] != value {
							t.Errorf("%s: server got %q, want [%q]", name, v, value)
						}
						continue
					}
					if err == nil || !strings.HasPrefix(err.Error(), "fishaudio: "+source+": value of header \"X-Probe\" has ") {
						t.Errorf("%s: err = %v, want the SDK's error", name, err)
					} else if leaksValue(err.Error(), value) {
						t.Errorf("%s: error %q shows the value", name, err)
					}
					if sent != 0 || got != nil {
						t.Errorf("%s: %d sent, server got %q", name, sent, got)
					}
				}
			}
		}
	}
}

func TestHeaderValues_IgnoredNamesAreNotChecked(t *testing.T) {
	p := newValueProbe(t)
	for _, path := range headerPaths {
		// From the provider: reserved names, and a name set per call.
		server, seen := headerServer(t, http.StatusOK)
		client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL),
			WithHeaderProvider(func(context.Context) (http.Header, error) {
				return http.Header{
					"Authorization":     {"a\r\nb"},
					"Host":              {"a\x00b"},
					"User-Agent":        {"a\r\nb"},
					"Content-Type":      {" x"},
					"Sec-WebSocket-Key": {"é"},
					"X-Per-Call":        {"a\nb"},
				}, nil
			}))
		ctx := ContextWithHeaders(context.Background(), http.Header{
			"Authorization": {"a\r\nb"},
			"Model":         {"a\x00b"},
			"Connection":    {"é"},
			"X-Per-Call":    {"ok"},
		})
		var err error
		if path == "ws" {
			err = dialTTS(ctx, client)
		} else {
			_, err = client.Account.GetCredits(ctx, nil)
		}
		if err != nil {
			t.Errorf("%s: err = %v", path, err)
		}
		got := seen()
		if len(got) != 1 {
			t.Fatalf("%s: %d requests", path, len(got))
		}
		h := got[0].header
		if h.Get("Authorization") != "Bearer test-key" || h.Get("User-Agent") != userAgent || h.Get("X-Per-Call") != "ok" {
			t.Errorf("%s: headers %q", path, h)
		}

		// A User-Agent per call is appended, not ignored, so it is checked.
		sent, _, err := p.send(perCallSource, path, http.Header{"User-Agent": {"a\r\nb"}})
		if want := `fishaudio: ContextWithHeaders: value of header "User-Agent" has a control character`; err == nil || err.Error() != want || sent != 0 {
			t.Errorf("%s: per-call User-Agent: err = %v, %d sent", path, err, sent)
		}
		_, got2, err := p.send(perCallSource, path, http.Header{"User-Agent": {"my-app/1.0 (x\ty)"}})
		if err != nil || got2.Get("User-Agent") != userAgent+" my-app/1.0 (x\ty)" {
			t.Errorf("%s: per-call User-Agent: err = %v, got %q", path, err, got2.Get("User-Agent"))
		}
	}
}

func TestHeaderValues_ProblemPrecedenceIgnoresPosition(t *testing.T) {
	const (
		whitespace = "leading or trailing whitespace"
		nonASCII   = "a non-ASCII character"
		control    = "a control character"
	)
	for value, want := range map[string]string{
		" a\x01":   control, // whitespace first, control later
		"é\x01":    control, // non-ASCII first, control later
		"\x7fé":    control,
		" a\x7f ":  control,
		"\t\x00\t": control,
		" é":       nonASCII, // whitespace first, non-ASCII later
		"é ":       nonASCII,
		"\tbé":     nonASCII,
		" a":       whitespace,
		"a\t":      whitespace,
		"a \tb":    "",
		"":         "",
		"\x01 é ":  control,
		"é\x01 ":   control,
		" éa\t":    nonASCII,
	} {
		if got := headerValueProblem(value); got != want {
			t.Errorf("headerValueProblem(%q) = %q, want %q", value, got, want)
		}
	}
	// And through both sources and both paths.
	p := newValueProbe(t)
	for _, source := range headerSources {
		for _, path := range headerPaths {
			_, _, err := p.send(source, path, http.Header{"X-Probe": {" a\x01"}})
			if want := "fishaudio: " + source + `: value of header "X-Probe" has a control character`; err == nil || err.Error() != want {
				t.Errorf("%s/%s: err = %v, want %q", source, path, err, want)
			}
		}
	}
}

// specialHeaderNames are the header names that net/http (Request.Write,
// Transport, Client), its HTTP/2 transport (net/http/internal/httpcommon)
// and gorilla/websocket's Dialer treat specially in a request, read from
// their sources, plus the names the SDK sets and some names that nothing
// treats specially. Spellings that are not canonical are on purpose.
var specialHeaderNames = []string{
	// net/http Request.Write and Transport
	"Host", "User-Agent", "Content-Length", "Transfer-Encoding", "Trailer",
	"Accept-Encoding", "Connection", "Expect", "Proxy-Authorization", "Cookie",
	// net/http Client (redirects and cookie jars)
	"Authorization", "Www-Authenticate", "Cookie2", "Referer",
	// HTTP/2
	"Proxy-Connection", "Keep-Alive", "Upgrade", "TE", "te",
	// gorilla/websocket
	"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol",
	"Sec-WebSocket-Accept",
	// The SDK
	"model", "Content-Type",
	// Representation metadata of the body
	"Content-Encoding", "Content-Language", "Content-Range", "Content-Md5",
	// Nothing treats these specially
	"Traceparent", "tracestate", "Baggage", "X-Request-Id", "Accept", "Sec-Fetch-Mode",
	"Via", "Forwarded", "Range", "Origin", "Priority", "Cache-Control", "Date", "Http2-Settings",
}

// TestHeaderNames_EveryNameOnEveryPath sends every name in specialHeaderNames
// from both sources over HTTP/1.1 and the WebSocket handshake (recorded byte
// for byte by a raw TCP server) and over HTTP/2 (TLS, recorded by a Go
// server). A name the SDK accepts must arrive once, with the value exactly as
// given, and the call must succeed; a reserved name must leave the request
// exactly as it is without it.
func TestHeaderNames_EveryNameOnEveryPath(t *testing.T) {
	rawURL, rawSeen := rawHeaderServer(t)
	var mu sync.Mutex
	var h2seen []*http.Request
	h2 := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h2seen = append(h2seen, r)
		mu.Unlock()
		_, _ = w.Write([]byte("{}"))
	}))
	h2.EnableHTTP2 = true
	h2.StartTLS()
	t.Cleanup(h2.Close)

	// send makes one call on path and returns the values of name that
	// arrived, and the call's error.
	send := func(path, name string, opts []ClientOption, ctx context.Context) ([]string, error) {
		switch path {
		case "h2":
			client := NewClient(append(opts, WithBaseURL(h2.URL), WithHTTPClient(h2.Client()))...)
			mu.Lock()
			n := len(h2seen)
			mu.Unlock()
			_, err := client.Account.GetCredits(ctx, nil)
			mu.Lock()
			defer mu.Unlock()
			if len(h2seen) == n {
				return nil, err
			}
			r := h2seen[len(h2seen)-1]
			if r.ProtoMajor != 2 {
				t.Fatalf("h2 server got %s", r.Proto)
			}
			if http.CanonicalHeaderKey(name) == "Host" {
				return []string{r.Host}, err
			}
			return r.Header.Values(name), err
		default:
			client := NewClient(append(opts, WithBaseURL(rawURL))...)
			n := len(rawSeen())
			var err error
			if path == "ws" {
				err = dialTTS(ctx, client)
			} else {
				_, err = client.Account.GetCredits(ctx, nil)
			}
			got := rawSeen()
			if len(got) == n {
				return nil, err
			}
			return got[len(got)-1].values(name), err
		}
	}

	values := []string{"x;a=1,b c", "100-continue", "trailers", "gzip", ""}
	for _, name := range specialHeaderNames {
		reserved := reservedHeader(http.CanonicalHeaderKey(name))
		for _, path := range []string{"http", "ws", "h2"} {
			base := []ClientOption{WithAPIKey("test-key")}
			without, err := send(path, name, base, context.Background())
			if err != nil {
				t.Fatalf("%s/%s without the header: %v", name, path, err)
			}
			for _, value := range values {
				for _, source := range headerSources {
					opts, ctx := base, context.Background()
					header := http.Header{name: {value}}
					if source == providerSource {
						opts = append(opts, WithHeaderProvider(func(context.Context) (http.Header, error) { return header, nil }))
					} else {
						ctx = ContextWithHeaders(ctx, header)
					}
					got, err := send(path, name, opts, ctx)
					label := fmt.Sprintf("%s: %q from %s over %s", name, value, source, path)
					switch {
					case err != nil:
						t.Errorf("%s: %v", label, err)
					case reserved && http.CanonicalHeaderKey(name) == "User-Agent" && source == perCallSource:
						// Appended to the SDK's; see TestHeaderValues_IgnoredNamesAreNotChecked.
					case reserved && http.CanonicalHeaderKey(name) == "Sec-Websocket-Key":
						// Random per handshake: one key, and not ours.
						if len(got) != len(without) || len(got) == 1 && got[0] == value {
							t.Errorf("%s: reserved, but the server got %q", label, got)
						}
					case reserved && fmt.Sprintf("%q", got) != fmt.Sprintf("%q", without):
						t.Errorf("%s: reserved, but the server got %q, want %q as without it", label, got, without)
					case !reserved && (len(got) != 1 || got[0] != value):
						t.Errorf("%s: server got %q, want [%q]", label, got, value)
					}
				}
			}
		}
	}
}

// TestHeaderNames_AcceptEncodingStaysWithTheTransport shows why
// Accept-Encoding is reserved besides the HTTP/1.1 duplicate the every-name
// test finds: net/http decompresses a gzip response only when it asked for
// gzip itself, so a provider's "Accept-Encoding: gzip" would hand the SDK
// compressed bytes to decode.
func TestHeaderNames_AcceptEncodingStaysWithTheTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			_, _ = w.Write([]byte(`{"credit":"1"}`))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte(`{"credit":"1"}`))
		_ = zw.Close()
	}))
	t.Cleanup(server.Close)
	for _, source := range headerSources {
		opts := []ClientOption{WithAPIKey("k"), WithBaseURL(server.URL)}
		ctx := context.Background()
		header := http.Header{"Accept-Encoding": {"gzip"}}
		if source == providerSource {
			opts = append(opts, WithHeaderProvider(func(context.Context) (http.Header, error) { return header, nil }))
		} else {
			ctx = ContextWithHeaders(ctx, header)
		}
		credits, err := NewClient(opts...).Account.GetCredits(ctx, nil)
		if err != nil || credits.Credit != "1" {
			t.Errorf("%s: credits %+v, err %v", source, credits, err)
		}
	}
}
