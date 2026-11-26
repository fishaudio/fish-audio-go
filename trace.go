package fishaudio

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

// traceIDHeader is the response header in which the Fish Audio API returns
// the trace id it used for a request: the caller's, when it continued the
// trace the request carried, or a new one.
const traceIDHeader = "X-Fish-Trace-Id"

// traceDocsURL explains how Fish Audio continues a caller's trace.
const traceDocsURL = "https://docs.fish.audio/api-reference/observability"

// traceWarned is set once the SDK has logged that the API used a trace other
// than the one the SDK set, so the warning is logged at most once per process.
var traceWarned atomic.Bool

// traceMismatchMessage is the warning's message. It states what the SDK saw
// and blames no one: the hint says what the SDK can tell about the cause.
const traceMismatchMessage = "fishaudio: the trace this SDK sent is not the one the API used"

// checkTrace compares the traceparent values the SDK put on a request (from
// ContextWithHeaders or the header provider) with the trace id in the
// response, and logs one warning per process through [slog.Default] when they
// differ. It does nothing when the SDK set no traceparent or the response has
// no trace id (another base URL, a proxy that drops it, no response at all).
// It never changes or fails the call.
//
// resp.Request is the request as the innermost transport received it: for
// net/http, the request http.Transport wrote, after any wrapping transport
// such as otelhttp changed its copy; for the WebSocket, the handshake request
// gorilla wrote. So the SDK can tell a traceparent replaced in this process
// from one that left it unchanged.
func checkTrace(ctx context.Context, sent []string, resp *http.Response) {
	if len(sent) == 0 || resp == nil || traceWarned.Load() {
		return
	}
	apiID := strings.TrimSpace(resp.Header.Get(traceIDHeader))
	if apiID == "" {
		return
	}
	var sentID string
	if len(sent) == 1 {
		sentID, _ = traceIDOf(sent[0])
	}
	if sentID != "" && strings.EqualFold(sentID, apiID) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// A logger that drops Warn must not use up the one warning: turning Warn
	// on later should still show it.
	if !slog.Default().Enabled(ctx, slog.LevelWarn) {
		return
	}
	if !traceWarned.CompareAndSwap(false, true) {
		return
	}

	// What left this process, if the SDK can see it.
	var outgoing []string
	visible := resp.Request != nil
	if visible {
		outgoing = resp.Request.Header.Values("Traceparent")
	}
	// A transport that only put in a child span of the same trace (otelhttp
	// does, given the span in ctx) did not replace the trace.
	replaced := visible && !equalValues(outgoing, sent) &&
		(sentID == "" || describeTrace(outgoing) != sentID)

	attrs := make([]any, 0, 8)
	attrs = append(attrs, slog.String("sent_trace_id", describeTrace(sent)))
	var hint string
	switch {
	case replaced:
		attrs = append(attrs, slog.String("outgoing_trace_id", describeTrace(outgoing)))
		hint = "the request left this process with a different trace from the one this SDK set: " +
			"an HTTP transport in this process, such as otelhttp, replaced the traceparent or added one; " +
			"have the provider send the trace of the span in the call's ctx, or leave the traceparent to that transport"
	case len(sent) > 1:
		hint = "the request carried " + strconv.Itoa(len(sent)) + " traceparent headers; send exactly one"
	case sentID == "":
		hint = "the traceparent is not one the API accepts; send a version-00 W3C traceparent, " +
			"00-<32 lowercase hex trace id>-<16 lowercase hex span id>-<2 hex flags>"
	case visible:
		hint = "the request left this process carrying the trace this SDK set, so the API did not use it, " +
			"or something between this process and the API, such as a proxy, replaced it"
	default:
		hint = "the transport did not return the request it sent, so this SDK cannot tell whether a transport " +
			"in this process, such as otelhttp, replaced the traceparent, or the API did not use it"
	}
	if !isLowerHex(strings.ToLower(apiID), 32) {
		apiID = "(not a trace id)"
	}
	attrs = append(attrs,
		slog.String("api_trace_id", apiID),
		slog.String("hint", hint+"; see "+traceDocsURL))
	if ctx == nil {
		ctx = context.Background()
	}
	slog.Default().WarnContext(ctx, traceMismatchMessage, attrs...)
}

// describeTrace returns the trace id of a single valid traceparent, or a
// description in parentheses. It never returns the values themselves: only
// trace ids are ever logged.
func describeTrace(values []string) string {
	switch len(values) {
	case 0:
		return "(no traceparent)"
	case 1:
		if id, ok := traceIDOf(values[0]); ok {
			return id
		}
		return "(not a valid traceparent)"
	}
	return "(" + strconv.Itoa(len(values)) + " traceparent values)"
}

func equalValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// traceIDOf returns the trace id of a version-00 W3C traceparent
// (00-<trace id>-<parent id>-<flags>), or false if value is not one. Like the
// Fish Audio API, it accepts only version 00 and only lowercase hex, and
// rejects all-zero ids; spaces and tabs around the value are ignored.
func traceIDOf(value string) (string, bool) {
	value = strings.Trim(value, " \t")
	if len(value) != 55 || value[:3] != "00-" || value[35] != '-' || value[52] != '-' {
		return "", false
	}
	traceID, parentID, flags := value[3:35], value[36:52], value[53:55]
	if !isLowerHex(traceID, 32) || !isLowerHex(parentID, 16) || !isLowerHex(flags, 2) ||
		traceID == strings.Repeat("0", 32) || parentID == strings.Repeat("0", 16) {
		return "", false
	}
	return traceID, true
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
