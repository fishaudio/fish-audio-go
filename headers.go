package fishaudio

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// HeaderProvider returns headers to add to an outgoing request, such as the
// W3C traceparent of the caller's current request, so Fish Audio continues
// the caller's trace instead of starting a new one.
//
// The SDK calls it once for every HTTP request and every WebSocket handshake,
// right before sending, on the goroutine that made the call, with the ctx
// passed to that call. Concurrent calls call it concurrently, so it must be
// safe for concurrent use. Keep it fast: the request waits for it.
//
// Return nil, nil to add nothing. A non-nil error fails the call before
// anything is sent, so a request never goes out without the provider's
// headers; the call's error wraps it, so [errors.Is] and [errors.As] find it.
// The SDK copies what it returns, so the provider may reuse or modify the
// returned http.Header afterwards. Headers the SDK sets (Authorization,
// model, User-Agent, Content-Type, Content-Length, Host, WebSocket handshake
// headers) cannot be set and are ignored, as is any name already set with
// [ContextWithHeaders]. A name or value that HTTP does not allow fails the
// call with an error before anything is sent. A panic is not recovered.
//
// The API returns the trace id it used in the X-Fish-Trace-Id response
// header. If the SDK sent a traceparent (from the provider or
// [ContextWithHeaders]) and the response names a different trace, the API
// did not continue it: the value was invalid, more than one was sent, or a
// transport replaced it. The SDK then logs one warning per process at
// [log/slog.LevelWarn] through [log/slog.Default], with both trace ids and
// the likely cause; it never echoes other header values and never changes
// the call.
//
// With OpenTelemetry (go.opentelemetry.io/otel/trace), this sends the span in
// the call's ctx as traceparent and no other trace data, so you can find your
// requests in Fish Audio's traces:
//
//	fishaudio.WithHeaderProvider(func(ctx context.Context) (http.Header, error) {
//		sc := trace.SpanContextFromContext(ctx)
//		if !sc.IsValid() {
//			return nil, nil
//		}
//		return http.Header{"Traceparent": {fmt.Sprintf("00-%s-%s-%s", sc.TraceID(), sc.SpanID(), sc.TraceFlags())}}, nil
//	})
type HeaderProvider func(ctx context.Context) (http.Header, error)

// WithHeaderProvider sets a function the SDK calls for every request and
// WebSocket handshake to add headers. See [HeaderProvider].
func WithHeaderProvider(provider HeaderProvider) ClientOption {
	return func(c *Client) {
		c.headerProvider = provider
	}
}

type requestHeadersKey struct{}

// ContextWithHeaders returns a copy of ctx that carries headers for every
// Fish Audio request made with it, HTTP and WebSocket alike: pass the result
// as the ctx of a call. Calling it again on the result adds to the headers;
// a name set again replaces the earlier values.
//
// These headers win over a [HeaderProvider]. A traceparent set here is
// checked against the response like the provider's; see [HeaderProvider].
// Headers the SDK sets (Authorization, model, Content-Type, Content-Length,
// Host, WebSocket handshake headers) cannot be replaced and are ignored; a
// User-Agent is appended to the SDK's own.
//
//	ctx = fishaudio.ContextWithHeaders(ctx, http.Header{"Traceparent": {tp}})
//	audio, err := client.TTS.Convert(ctx, &fishaudio.ConvertParams{Text: "Hello"})
func ContextWithHeaders(ctx context.Context, headers http.Header) context.Context {
	merged := http.Header{}
	if existing, ok := ctx.Value(requestHeadersKey{}).(http.Header); ok {
		for name, values := range existing {
			merged[name] = values
		}
	}
	for name, values := range headers {
		merged[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	return context.WithValue(ctx, requestHeadersKey{}, merged)
}

// reservedHeader reports whether name is a header the SDK sets itself or that
// HTTP and the WebSocket handshake own. Neither a provider nor per-call
// headers can set it, so neither can replace the API key or the model.
func reservedHeader(canonical string) bool {
	switch canonical {
	case "Authorization", "Model", "User-Agent", "Content-Type", "Content-Length",
		"Host", "Connection", "Upgrade", "Transfer-Encoding":
		return true
	}
	return strings.HasPrefix(canonical, "Sec-Websocket-")
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return false
		}
	}
	return true
}

// isTokenChar reports whether c may appear in an RFC 9110 token.
func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < ' ' && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}

func checkHeader(source, name string, values []string) error {
	if !validHeaderName(name) {
		return fmt.Errorf("fishaudio: %s has an invalid header name %q", source, name)
	}
	for _, value := range values {
		if !validHeaderValue(value) {
			// The value is not echoed: it may be a credential.
			return fmt.Errorf("fishaudio: %s has an invalid value for header %q", source, name)
		}
	}
	return nil
}

// addHeaders adds the per-call headers carried by ctx, then the header
// provider's, to header, which already holds the SDK's own headers. Every
// request and WebSocket handshake calls it right before sending, so the
// provider sees the caller's ctx at that moment. (A retry loop added later
// must call it once per attempt.)
//
// It returns the Traceparent values it put on header, nil if it put none, for
// [checkTrace] to compare with the response.
func (c *Client) addHeaders(ctx context.Context, header http.Header) ([]string, error) {
	var traceparent []string
	if perCall, ok := ctx.Value(requestHeadersKey{}).(http.Header); ok {
		for name, values := range perCall {
			if err := checkHeader("ContextWithHeaders", name, values); err != nil {
				return nil, err
			}
			switch {
			case name == "User-Agent":
				header.Set("User-Agent", userAgentWith(strings.Join(values, " ")))
			case reservedHeader(name):
			case len(values) > 0:
				header[name] = append([]string(nil), values...)
				if name == "Traceparent" {
					traceparent = header[name]
				}
			}
		}
	}
	if c.headerProvider == nil {
		return append([]string(nil), traceparent...), nil
	}
	provided, err := c.headerProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("fishaudio: header provider: %w", err)
	}
	// Canonical names first, so two spellings of one name cannot both be sent.
	canonical := http.Header{}
	for name, values := range provided {
		if err := checkHeader("header provider", name, values); err != nil {
			return nil, err
		}
		key := http.CanonicalHeaderKey(name)
		canonical[key] = append(canonical[key], values...)
	}
	for name, values := range canonical {
		if reservedHeader(name) || len(values) == 0 {
			continue
		}
		if _, taken := header[name]; taken {
			continue
		}
		header[name] = values
		if name == "Traceparent" {
			traceparent = values
		}
	}
	// A copy, so a transport that edits the request's headers cannot change
	// what is compared.
	return append([]string(nil), traceparent...), nil
}
