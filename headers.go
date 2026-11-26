package fishaudio

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// HeaderProvider returns headers to add to an outgoing request, such as the
// W3C traceparent of the caller's current request, so Fish Audio continues
// the caller's trace instead of starting a new one.
//
// The SDK calls it once for every SDK call that sends an HTTP request or
// opens a WebSocket, right before sending, on the goroutine that made the
// call, with the ctx passed to that call. If the server redirects, net/http
// sends the same headers again without calling it a second time. Concurrent calls call it concurrently, so it must be
// safe for concurrent use. Keep it fast: the request waits for it.
//
// Return nil, nil to add nothing. A non-nil error fails the call before
// anything is sent; the call's error wraps it, so [errors.Is] and [errors.As]
// find it. A panic is not recovered.
//
// The SDK reads the returned http.Header after the provider returns, without
// any lock the provider holds, so the provider must not modify that header
// or its value slices afterwards. Building a new http.Header on every call
// is the simple way to follow this. Returning the same header every time is
// also fine if nothing ever modifies it: the SDK only reads it.
//
// Names that differ only in case (traceparent and Traceparent) are one
// header: their values are combined in the byte order of the spellings
// (Traceparent before traceparent), each spelling's values in the order
// given. A name with no values adds nothing. Names the SDK or the transport
// owns are ignored whatever their value: Authorization, model, User-Agent,
// Host, Cookie, Expect, Accept-Encoding, every Content-* name (the SDK
// writes the body), the hop-by-hop Connection, Keep-Alive,
// Proxy-Connection, TE, Trailer, Transfer-Encoding and Upgrade, and every
// Sec-WebSocket-* name. So is any name set with [ContextWithHeaders], with or
// without values.
//
// A header name must be an RFC 9110 token. A value must be empty, or visible
// ASCII (0x21-0x7E) with spaces and tabs only between visible characters: no
// leading or trailing whitespace, no control characters, no non-ASCII bytes.
// Any other name or value fails the call before anything is sent, with an
// error that names the header and the problem but never shows the value.
// Every name that is not ignored reaches the server with every value
// accepted exactly as given, over HTTP/1.1, HTTP/2 and the WebSocket
// handshake alike (a User-Agent from [ContextWithHeaders] after the SDK's
// own), unless an HTTP transport you configure changes it.
//
// The API returns the trace id it used in the X-Fish-Trace-Id response
// header. If the SDK set a traceparent (from the provider or
// [ContextWithHeaders]) and the response names a different trace, the SDK
// logs one warning per process at [log/slog.LevelWarn] through
// [log/slog.Default], with the trace ids and what the SDK can tell about
// the cause: the value was invalid, more than one was set, a transport in
// this process (such as otelhttp) replaced it before sending, or it left
// this process unchanged and the API did not use it or a proxy on the way
// replaced it. It never echoes other header values and never changes the
// call.
//
// With OpenTelemetry (go.opentelemetry.io/otel/trace), this provider adds the
// span in the call's ctx as a traceparent, so you can find your requests in
// Fish Audio's traces. It adds no tracestate or baggage; an otelhttp transport,
// if you also use one, still adds whatever its propagator sends.
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

// Sources named in the errors for headers that may not be sent.
const (
	providerSource = "header provider"
	perCallSource  = "ContextWithHeaders"
)

// ContextWithHeaders returns a copy of ctx that carries headers for every
// Fish Audio request made with it, HTTP and WebSocket alike: pass the result
// as the ctx of a call. Calling it again on the result adds to the headers;
// a name set again replaces the earlier values.
//
// These headers win over a [HeaderProvider]: the provider's value for a name
// set here is never sent. A name set with no values (nil or an empty slice)
// sends nothing for that name, so it keeps the provider's value off this
// call. Names that differ only in case are one header, combined as described
// for [HeaderProvider]. A traceparent set here is checked against the
// response like the provider's; see [HeaderProvider]. Names the SDK or the
// transport owns (as listed for [HeaderProvider]) cannot be replaced and are
// ignored whatever their value, except that a User-Agent is appended to the
// SDK's own. Names and values follow the rule given for [HeaderProvider].
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
	for name, values := range canonicalHeader(headers) {
		merged[name] = values
	}
	return context.WithValue(ctx, requestHeadersKey{}, merged)
}

// sortedNames returns the names in h in byte order, so that everything done
// name by name (combining spellings, choosing which error to return) does
// not depend on map iteration order.
func sortedNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// canonicalHeader returns a copy of h keyed by canonical names. The values of
// names that differ only in case are combined in the byte order of the
// spellings, so the result never depends on map iteration order. A name with
// no values stays in the result with none. The value slices are new, so
// nothing done to the result can change h, and the reverse.
func canonicalHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for _, name := range sortedNames(h) {
		key := http.CanonicalHeaderKey(name)
		out[key] = append(out[key], h[name]...)
	}
	return out
}

// reservedHeader reports whether canonical is a header name the SDK or the
// transport owns. Neither a provider nor per-call headers can set it, so
// neither can replace the API key or the model, nor send a header that
// net/http, its HTTP/2 transport or the WebSocket handshake would drop,
// rewrite or act on. TestHeaderNames_EveryNameOnEveryPath sends every name
// these treat specially over every path, so a change here is checked.
func reservedHeader(canonical string) bool {
	switch canonical {
	// Set by the SDK.
	case "Authorization", "Model", "User-Agent",
		// Owned by the transport: Host is the URL's; Request.Write never
		// sends Trailer from the header map; Expect and Accept-Encoding
		// change how the transport sends the body and reads the response;
		// Cookie belongs to a cookie jar, and HTTP/2 splits it and so
		// changes its bytes.
		"Host", "Trailer", "Expect", "Accept-Encoding", "Cookie",
		// The transport adds its own from the proxy URL's credentials, so a
		// second one would reach the proxy.
		"Proxy-Authorization",
		// Hop-by-hop (RFC 9110 §7.6.1): not end to end, and HTTP/2 drops
		// them or fails the request.
		"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Transfer-Encoding", "Upgrade":
		return true
	}
	// Content-* describes the body, which the SDK writes; Sec-WebSocket-*
	// belongs to the WebSocket handshake.
	return strings.HasPrefix(canonical, "Content-") || strings.HasPrefix(canonical, "Sec-Websocket-")
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

// headerValueProblem returns "" if value may be sent, else the kind of
// problem, for an error that names the header but never shows the value,
// which may be a credential.
//
// The rule, shared with the other Fish Audio SDKs (change them together), is
// an RFC 9110 §5.5 field value made of ASCII only. A value is valid if it is
// empty, or if it is visible ASCII (0x21-0x7E) with spaces and tabs only
// between visible characters. Every value it accepts, under a name that is not
// reserved, reaches the server byte for byte, over net/http (HTTP/1.1 and
// HTTP/2) and over the WebSocket handshake alike.
//
// When a value has more than one problem, the one reported is, in this
// order and wherever the bytes are: a control character, a non-ASCII
// character, leading or trailing whitespace.
//
// It is stricter than net/http on purpose. net/http sends non-ASCII bytes
// (obs-text), which RFC 9110 keeps only for compatibility and which a server
// or proxy may reject or decode differently, and it silently trims leading
// and trailing whitespace, so the server would not get the value as given.
// Control characters (including CR, LF, NUL and DEL) are rejected by both.
func headerValueProblem(value string) string {
	var control, nonASCII bool
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c >= 0x80:
			nonASCII = true
		case c < 0x20 && c != '\t' || c == 0x7f:
			control = true
		}
	}
	switch {
	case control:
		return "a control character"
	case nonASCII:
		return "a non-ASCII character"
	case value != "" && (isSpaceOrTab(value[0]) || isSpaceOrTab(value[len(value)-1])):
		return "leading or trailing whitespace"
	}
	return ""
}

func isSpaceOrTab(c byte) bool { return c == ' ' || c == '\t' }

// checkHeaderValues returns an error for the first value of the header name
// that may not be sent. The error names the source and the header and says
// what is wrong, but never contains the value or any part of it.
func checkHeaderValues(source, name string, values []string) error {
	for _, value := range values {
		if problem := headerValueProblem(value); problem != "" {
			return fmt.Errorf("fishaudio: %s: value of header %q has %s", source, name, problem)
		}
	}
	return nil
}

func checkHeaderName(source, name string) error {
	if !validHeaderName(name) {
		return fmt.Errorf("fishaudio: %s: invalid header name %q", source, name)
	}
	return nil
}

// addHeaders adds the per-call headers carried by ctx, then the header
// provider's, to header, which already holds the SDK's own headers. Every
// request and WebSocket handshake calls it right before sending, so the
// provider sees the caller's ctx at that moment. (A retry loop added later
// must call it once per attempt.) Names are handled in byte order, so the
// error returned for several bad headers is always the same one.
//
// It returns the Traceparent values it put on header, nil if it put none, for
// [checkTrace] to compare with the response.
func (c *Client) addHeaders(ctx context.Context, header http.Header) ([]string, error) {
	var traceparent []string
	// Keyed by canonical name already (see ContextWithHeaders).
	perCall, _ := ctx.Value(requestHeadersKey{}).(http.Header)
	for _, name := range sortedNames(perCall) {
		values := perCall[name]
		if err := checkHeaderName(perCallSource, name); err != nil {
			return nil, err
		}
		// A reserved name is ignored, whatever its value: it is skipped
		// before its value is checked. User-Agent is appended, not ignored,
		// so its value is checked.
		if reservedHeader(name) && name != "User-Agent" {
			continue
		}
		if err := checkHeaderValues(perCallSource, name, values); err != nil {
			return nil, err
		}
		switch {
		case len(values) == 0:
			// Nothing is sent for this name, and the provider's value is
			// not sent either: the name is in perCall, checked below.
		case name == "User-Agent":
			header.Set("User-Agent", userAgentWith(strings.Join(values, " ")))
		default:
			header[name] = append([]string(nil), values...)
			if name == "Traceparent" {
				traceparent = header[name]
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
	for _, name := range sortedNames(provided) {
		if err := checkHeaderName(providerSource, name); err != nil {
			return nil, err
		}
	}
	// Canonical names, so two spellings of one name cannot both be sent; the
	// values are copies, so the SDK never writes into the provider's header.
	canonical := canonicalHeader(provided)
	for _, name := range sortedNames(canonical) {
		values := canonical[name]
		if reservedHeader(name) || len(values) == 0 {
			continue
		}
		// A name already set by the SDK, or given per call with or without
		// values, is ignored, whatever its value: it is skipped before its
		// value is checked.
		if _, taken := header[name]; taken {
			continue
		}
		if _, taken := perCall[name]; taken {
			continue
		}
		if err := checkHeaderValues(providerSource, name, values); err != nil {
			return nil, err
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
