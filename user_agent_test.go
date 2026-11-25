package fishaudio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// RFC 9110 section 5.6.2 tchar; a product is token ["/" product-version].
var productGrammar = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+/[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

func TestUserAgent_Format(t *testing.T) {
	if userAgent != "fish-audio-go/"+Version {
		t.Errorf("userAgent = %q, want fish-audio-go/%s", userAgent, Version)
	}
	if !productGrammar.MatchString(userAgent) {
		t.Errorf("userAgent %q is not an RFC 9110 product (token \"/\" token)", userAgent)
	}
}

func TestUserAgent_VersionMatchesReleaseManifest(t *testing.T) {
	data, err := os.ReadFile(".release-please-manifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if manifest["."] != Version {
		t.Errorf("Version = %q, but .release-please-manifest.json says %q", Version, manifest["."])
	}
}

func TestUserAgentWith(t *testing.T) {
	tests := []struct{ caller, want string }{
		{"", userAgent},
		{"   ", userAgent},
		{"my-app/1.0", userAgent + " my-app/1.0"},
		{userAgent, userAgent},
		{userAgent + " my-app/1.0", userAgent + " my-app/1.0"},
	}
	for _, tt := range tests {
		if got := userAgentWith(tt.caller); got != tt.want {
			t.Errorf("userAgentWith(%q) = %q, want %q", tt.caller, got, tt.want)
		}
	}
}

// recordingServer answers every request with status and records the
// User-Agent values each one carried.
func recordingServer(t *testing.T, status int) (*httptest.Server, func() map[string][]string) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string][]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Method+" "+r.URL.Path] = r.Header.Values("User-Agent")
		mu.Unlock()
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
	t.Cleanup(server.Close)
	return server, func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[string][]string{}
		for k, v := range seen {
			out[k] = v
		}
		return out
	}
}

func TestUserAgent_EveryHTTPPath(t *testing.T) {
	server, seen := recordingServer(t, http.StatusOK)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	ctx := context.Background()

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

	got := seen()
	want := []string{
		"GET /wallet/self/api-credit", "GET /wallet/self/package",
		"GET /model", "GET /model/v1", "POST /model", "PATCH /model/v1", "DELETE /model/v1",
		"POST /v1/tts", "POST /v1/asr",
	}
	for _, key := range want {
		values, ok := got[key]
		if !ok {
			t.Errorf("%s was not requested (saw %v)", key, got)
			continue
		}
		if len(values) != 1 || values[0] != userAgent {
			t.Errorf("%s sent User-Agent %q, want exactly [%q]", key, values, userAgent)
		}
	}
}

func TestUserAgent_FailingRequestSentItAndReturnsTheError(t *testing.T) {
	server, seen := recordingServer(t, http.StatusInternalServerError)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	if _, err := client.Voices.List(context.Background(), nil); err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
	if _, err := client.ASR.Transcribe(context.Background(), []byte{0}, nil); err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
	for key, values := range seen() {
		if len(values) != 1 || values[0] != userAgent {
			t.Errorf("%s sent User-Agent %q, want exactly [%q]", key, values, userAgent)
		}
	}
}

func TestUserAgent_AdditionalHeaderIsAppendedInAnyCasing(t *testing.T) {
	server, seen := recordingServer(t, http.StatusOK)
	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))

	resp, err := client.doRequest(context.Background(), http.MethodGet, "/model", nil,
		&RequestOptions{AdditionalHeaders: map[string]string{"user-agent": "my-app/1.0"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	values := seen()["GET /model"]
	if len(values) != 1 || values[0] != userAgent+" my-app/1.0" {
		t.Errorf("User-Agent = %q, want exactly [%q]", values, userAgent+" my-app/1.0")
	}
}

func TestUserAgent_TTSWebSocketHandshake(t *testing.T) {
	headers := make(chan http.Header, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	textChan := make(chan string)
	close(textChan)
	stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, &StreamParams{}, nil)
	if err == nil {
		_ = stream.Close()
	}

	h := <-headers
	if got := h.Values("User-Agent"); len(got) != 1 || got[0] != userAgent {
		t.Errorf("handshake User-Agent = %q, want exactly [%q]", got, userAgent)
	}
	if got := h.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestUserAgent_RefusedTTSWebSocketHandshakeSentIt(t *testing.T) {
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
	textChan := make(chan string)
	close(textChan)
	if _, err := client.TTS.StreamWebSocket(context.Background(), textChan, &StreamParams{}, nil); err == nil {
		t.Fatal("expected an error for a refused handshake")
	}

	if got := (<-headers).Values("User-Agent"); len(got) != 1 || got[0] != userAgent {
		t.Errorf("handshake User-Agent = %q, want exactly [%q]", got, userAgent)
	}
}
