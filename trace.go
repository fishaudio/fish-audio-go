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

// traceWarned is set once the SDK has logged that the API did not continue
// a trace, so the warning is logged at most once per process.
var traceWarned atomic.Bool

// checkTrace compares the traceparent values the SDK put on a request (from
// ContextWithHeaders or the header provider) with the trace id in the
// response, and logs one warning per process through [slog.Default] when the
// API did not continue that trace. It does nothing when the SDK sent no
// traceparent or the response has no trace id (another base URL, a proxy
// that drops it, no response at all). It never changes or fails the call.
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
	if !traceWarned.CompareAndSwap(false, true) {
		return
	}

	attrs := make([]any, 0, 6)
	var hint string
	switch {
	case len(sent) > 1:
		attrs = append(attrs, slog.String("sent_trace_id", "("+strconv.Itoa(len(sent))+" traceparent values)"))
		hint = "the request carried " + strconv.Itoa(len(sent)) + " traceparent headers; send exactly one"
	case sentID == "":
		// The value is not echoed: only the two trace ids are ever logged.
		attrs = append(attrs, slog.String("sent_trace_id", "(not a valid traceparent)"))
		hint = "the traceparent is not one the API accepts; send a version-00 W3C traceparent, " +
			"00-<32 lowercase hex trace id>-<16 lowercase hex span id>-<2 hex flags>"
	default:
		attrs = append(attrs, slog.String("sent_trace_id", sentID))
		hint = "something between this SDK and the API replaced the traceparent or added a second one, " +
			"usually an HTTP transport such as otelhttp putting in its own span's, from another trace; " +
			"send the trace of the span in the call's ctx"
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
	slog.Default().WarnContext(ctx, "fishaudio: the API did not continue the trace this SDK sent", attrs...)
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
