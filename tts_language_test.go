package fishaudio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// languageCases covers how Language reaches the wire. want "" means the key
// must be absent, not sent as an empty string or null.
var languageCases = []struct {
	name   string
	params StreamParams
	want   string
}{
	{"unset", StreamParams{}, ""},
	{"unset with config", StreamParams{Config: &TTSConfig{Format: AudioFormatWAV}}, ""},
	{"parameter", StreamParams{Language: "es-MX"}, "es-MX"},
	{"config", StreamParams{Config: &TTSConfig{Language: "zh-CN"}}, "zh-CN"},
	{"parameter overrides config", StreamParams{Language: "es-MX", Config: &TTSConfig{Language: "en"}}, "es-MX"},
	{"sent as given", StreamParams{Language: "ES_mx"}, "ES_mx"},
}

func checkLanguage(t *testing.T, body map[string]interface{}, want string) {
	t.Helper()
	got, present := body["language"]
	if want == "" {
		if present {
			t.Errorf("language = %#v, want the key absent", got)
		}
		return
	}
	if got != want {
		t.Errorf("language = %#v, want %q", got, want)
	}
}

func TestTTSService_Language_HTTP(t *testing.T) {
	for _, tc := range languageCases {
		for _, entry := range []string{"Stream", "Convert"} {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				var body map[string]interface{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					if err := json.Unmarshal(raw, &body); err != nil {
						t.Errorf("request body is not JSON: %v", err)
					}
					_, _ = w.Write([]byte("audio"))
				}))
				defer server.Close()

				client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
				params := tc.params
				params.Text = "La entrada cuesta $300."
				var err error
				if entry == "Stream" {
					var stream *AudioStream
					stream, err = client.TTS.Stream(context.Background(), &params)
					if err == nil {
						_, err = stream.Collect()
					}
				} else {
					_, err = client.TTS.Convert(context.Background(), &ConvertParams{
						Text:     params.Text,
						Language: params.Language,
						Config:   params.Config,
					})
				}
				if err != nil {
					t.Fatalf("%s() error = %v", entry, err)
				}
				checkLanguage(t, body, tc.want)
			})
		}
	}
}

func TestTTSService_Language_WebSocketStartEvent(t *testing.T) {
	for _, tc := range languageCases {
		t.Run(tc.name, func(t *testing.T) {
			var start map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := wsUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err := msgpack.Unmarshal(data, &start); err != nil {
					t.Errorf("unmarshal start: %v", err)
				}
				for {
					_, data, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var msg map[string]interface{}
					_ = msgpack.Unmarshal(data, &msg)
					if msg["event"] == "stop" {
						break
					}
				}
				for _, resp := range []wsResponse{{Event: "audio", Audio: []byte("audio")}, {Event: "finish", Reason: "stop"}} {
					out, _ := msgpack.Marshal(resp)
					_ = conn.WriteMessage(websocket.BinaryMessage, out)
				}
			}))
			defer server.Close()

			client := NewClient(WithAPIKey("test-key"), WithBaseURL(server.URL))
			textChan := make(chan string, 1)
			textChan <- "Hola"
			close(textChan)
			params := tc.params
			stream, err := client.TTS.StreamWebSocket(context.Background(), textChan, &params, nil)
			if err != nil {
				t.Fatalf("StreamWebSocket() error = %v", err)
			}
			if _, err := stream.Collect(); err != nil {
				t.Fatalf("Collect() error = %v", err)
			}

			if start["event"] != "start" {
				t.Fatalf("first message = %#v, want a start event", start)
			}
			request, ok := start["request"].(map[string]interface{})
			if !ok {
				t.Fatalf("start request = %#v, want a map", start["request"])
			}
			checkLanguage(t, request, tc.want)
		})
	}
}
