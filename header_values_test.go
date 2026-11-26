package fishaudio

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
