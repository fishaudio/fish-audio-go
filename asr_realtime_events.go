package fishaudio

import (
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

// Bool returns a pointer to v. Optional boolean settings are pointers so that
// "not set" (nil, the server default applies) differs from false.
func Bool(v bool) *bool { return &v }

// ASRTurnDetectionMode selects how the server decides that a turn has ended.
type ASRTurnDetectionMode string

const (
	// ASRTurnDetectionAuto lets the server's turn detection decide when the
	// user has finished, and tell user turns from noise and other voices.
	// This is the default.
	ASRTurnDetectionAuto ASRTurnDetectionMode = "auto"
	// ASRTurnDetectionVAD ends a turn after SilenceTimeoutMS of silence.
	ASRTurnDetectionVAD ASRTurnDetectionMode = "vad"
	// ASRTurnDetectionManual ends a turn when you call Finalize (push-to-talk).
	ASRTurnDetectionManual ASRTurnDetectionMode = "manual"
)

// ASRTurnDetection configures turn detection. Zero-valued fields are left out,
// so the server default (in ASRRealtimeParams) or the current value (in
// ASRConfigureParams) applies.
type ASRTurnDetection struct {
	// Mode is the turn detection mode. Default: ASRTurnDetectionAuto.
	Mode ASRTurnDetectionMode `msgpack:"mode,omitempty"`
	// Eager sends ASREagerEndOfTurnEvent in auto mode as soon as a turn has
	// probably ended. Default: false.
	Eager *bool `msgpack:"eager,omitempty"`
	// SilenceTimeoutMS is the silence that ends a turn (vad), or a safety net
	// (auto, manual). Defaults: auto 5000, vad 700, manual 60000.
	SilenceTimeoutMS int `msgpack:"silence_timeout_ms,omitempty"`
}

// ASRResults selects what the server sends back. A nil field is left out, so
// the server default (in ASRRealtimeParams) or the current value (in
// ASRConfigureParams) applies.
type ASRResults struct {
	// Drafts sends live ASRDraftEvent messages while a turn is open.
	// Default: false.
	Drafts *bool `msgpack:"drafts,omitempty"`
	// IgnoreTimestamps set to false adds Words with timings to
	// ASRCommittedEvent. Default: true.
	IgnoreTimestamps *bool `msgpack:"ignore_timestamps,omitempty"`
	// Diarize controls speaker tags such as "<|speaker:0|>" in the text.
	// Default: true. Set it to Bool(false) for plain text.
	Diarize *bool `msgpack:"diarize,omitempty"`
	// TagAudioEvents controls sound tags such as "[laughter]" in the text.
	// Default: true.
	TagAudioEvents *bool `msgpack:"tag_audio_events,omitempty"`
	// VADEvents sends ASRVADEvent messages. Default: false.
	VADEvents *bool `msgpack:"vad_events,omitempty"`
	// EOTEvents sends ASREOTProbEvent messages. Default: false.
	EOTEvents *bool `msgpack:"eot_events,omitempty"`
}

// ASRContextRole is the speaker of an ASRContextItem.
type ASRContextRole string

const (
	ASRContextRoleUser      ASRContextRole = "user"
	ASRContextRoleAssistant ASRContextRole = "assistant"
)

// ASRContextItem is one line of conversation, used as context for recognition.
type ASRContextItem struct {
	// Role is who said it.
	Role ASRContextRole `msgpack:"role"`
	// Text is what was said, up to 2000 characters.
	Text string `msgpack:"text"`
}

// ASRContext gives the model context when a session starts.
type ASRContext struct {
	// History is earlier conversation, oldest first, up to 32 items.
	History []ASRContextItem `msgpack:"history,omitempty"`
	// Terms are names, products and jargon to recognize: up to 100 terms of
	// up to 32 characters.
	Terms []string `msgpack:"terms,omitempty"`
}

// ASRRealtimeParams contains parameters for a realtime speech-to-text session.
// The zero value is a valid session: 16 kHz audio, auto turn detection, and
// the server's defaults for everything else.
type ASRRealtimeParams struct {
	// Model is the realtime model, sent as the "model" header.
	// Default: ASRModelTranscribeStream1.
	Model ASRModel
	// SampleRate is the sample rate of your audio in Hz: 8000, 12000, 16000,
	// 22050, 24000, 32000, 44100 or 48000. The server converts it, so do not
	// resample. Default: 16000.
	SampleRate int
	// TurnDetection configures when a turn ends.
	TurnDetection *ASRTurnDetection
	// Results selects what the server sends back. Transcripts carry speaker
	// tags such as "<|speaker:0|>" unless Results.Diarize is Bool(false).
	Results *ASRResults
	// Context gives the model vocabulary and earlier conversation.
	Context *ASRContext
	// ClientReferenceID is your own ID for the session, up to 256 characters.
	ClientReferenceID string
}

// ASRConfigureParams changes settings during a session. Everything left out
// keeps its current value.
type ASRConfigureParams struct {
	// TurnDetection changes turn detection. Switching Mode without
	// SilenceTimeoutMS resets the timeout to the new mode's default.
	TurnDetection *ASRTurnDetection
	// Results changes what the server sends back.
	Results *ASRResults
	// Terms replaces the list of terms. nil keeps the current list; an empty,
	// non-nil slice clears it.
	Terms []string
}

// Client messages.

type asrAudioFormat struct {
	Format     string `msgpack:"format"`
	SampleRate int    `msgpack:"sample_rate"`
	Channels   int    `msgpack:"channels"`
}

type asrConfigMessage struct {
	Type              string            `msgpack:"type"`
	Audio             asrAudioFormat    `msgpack:"audio"`
	TurnDetection     *ASRTurnDetection `msgpack:"turn_detection,omitempty"`
	Results           *ASRResults       `msgpack:"results,omitempty"`
	Context           *ASRContext       `msgpack:"context,omitempty"`
	ClientReferenceID string            `msgpack:"client_reference_id,omitempty"`
}

// asrAudioMessage carries PCM. Data is a []byte so that it is encoded as
// MessagePack bin: the server drops audio sent as a string or an array.
type asrAudioMessage struct {
	Type string `msgpack:"type"`
	Data []byte `msgpack:"data"`
}

// asrSimpleMessage is finalize, keepalive and audio_done.
type asrSimpleMessage struct {
	Type string `msgpack:"type"`
}

type asrAgentSpeakingMessage struct {
	Type     string `msgpack:"type"`
	Speaking bool   `msgpack:"speaking"`
}

type asrContextAddMessage struct {
	Type         string           `msgpack:"type"`
	Items        []ASRContextItem `msgpack:"items"`
	ResetHistory bool             `msgpack:"reset_history,omitempty"`
}

type asrTermsUpdate struct {
	Terms []string `msgpack:"terms"`
}

type asrConfigureMessage struct {
	Type          string            `msgpack:"type"`
	TurnDetection *ASRTurnDetection `msgpack:"turn_detection,omitempty"`
	Results       *ASRResults       `msgpack:"results,omitempty"`
	Context       *asrTermsUpdate   `msgpack:"context,omitempty"`
}

func newASRConfigMessage(params *ASRRealtimeParams) *asrConfigMessage {
	sampleRate := params.SampleRate
	if sampleRate == 0 {
		sampleRate = 16000
	}
	return &asrConfigMessage{
		Type:              "config",
		Audio:             asrAudioFormat{Format: "pcm_s16le", SampleRate: sampleRate, Channels: 1},
		TurnDetection:     params.TurnDetection,
		Results:           params.Results,
		Context:           params.Context,
		ClientReferenceID: params.ClientReferenceID,
	}
}

func newASRConfigureMessage(params *ASRConfigureParams) *asrConfigureMessage {
	msg := &asrConfigureMessage{
		Type:          "configure",
		TurnDetection: params.TurnDetection,
		Results:       params.Results,
	}
	if params.Terms != nil {
		// A non-nil slice, so that an empty list is sent as [] and not as nil.
		msg.Context = &asrTermsUpdate{Terms: append([]string{}, params.Terms...)}
	}
	return msg
}

// Server events.

// ASREvent is a message from the server in a realtime speech-to-text session.
// Use a type switch on the concrete event types:
//
//	switch e := stream.Event().(type) {
//	case *fishaudio.ASRDraftEvent:
//	    fmt.Println("...", e.Text)
//	case *fishaudio.ASRCommittedEvent:
//	    fmt.Println(e.Text)
//	}
//
// Fields the server left out have their zero value.
type ASREvent interface {
	// EventType returns the message type on the wire, such as "committed".
	EventType() string
}

// ASRLimits are the limits of a session.
type ASRLimits struct {
	// MaxSessionMS is the maximum session length in milliseconds.
	MaxSessionMS int `msgpack:"max_session_ms"`
	// MaxTurnMS is the maximum turn length in milliseconds.
	MaxTurnMS int `msgpack:"max_turn_ms"`
	// MaxFrameBytes is the maximum size of one message in bytes.
	MaxFrameBytes int `msgpack:"max_frame_bytes"`
}

// ASRWord is one word of a committed transcript, with its timing.
type ASRWord struct {
	Text    string `msgpack:"text"`
	StartMS int    `msgpack:"start_ms"`
	EndMS   int    `msgpack:"end_ms"`
}

// ASRSessionStartedEvent says the session is ready. It is always the first
// event of a stream.
type ASRSessionStartedEvent struct {
	// Seq is the message sequence number, increasing by one per server message.
	Seq int `msgpack:"seq"`
	// SessionID is the session's ID.
	SessionID string `msgpack:"session_id"`
	// RequestID identifies the session to Fish Audio support.
	RequestID string `msgpack:"request_id"`
	// Config is the configuration in effect, with defaults filled in.
	Config map[string]interface{} `msgpack:"config"`
	// Limits are the limits of the session.
	Limits ASRLimits `msgpack:"limits"`
}

// ASRSpeechStartEvent says a turn opened on voice activity. In auto mode,
// noise and other voices open turns too: wait for ASRUserTurnTakenEvent before
// treating it as the user's turn.
type ASRSpeechStartEvent struct {
	Seq int `msgpack:"seq"`
	// TurnIndex is the turn that opened, counting from 0.
	TurnIndex int `msgpack:"turn_index"`
	// AudioMS is where the turn starts, slightly before the speech.
	AudioMS int `msgpack:"audio_ms"`
}

// ASRUserTurnTakenEvent says the server judged that the user is taking their
// turn (auto mode only). For a voice agent that is speaking, this is the
// signal to stop.
type ASRUserTurnTakenEvent struct {
	Seq       int `msgpack:"seq"`
	TurnIndex int `msgpack:"turn_index"`
	// AudioMS is where the server judged that the user took the turn.
	AudioMS int `msgpack:"audio_ms"`
}

// ASRDraftEvent is a live draft of the open turn, sent when Results.Drafts is
// on. It is not final: take the final text from ASRCommittedEvent.
type ASRDraftEvent struct {
	Seq       int `msgpack:"seq"`
	TurnIndex int `msgpack:"turn_index"`
	// Text is the whole turn so far. It replaces the previous draft.
	Text string `msgpack:"text"`
	// SettledText is the beginning of Text that the server is confident will
	// not change. Often empty.
	SettledText string `msgpack:"settled_text"`
	// Language is the English name of the detected language, if identified.
	Language string `msgpack:"language"`
	// LanguageCode is the ISO 639-1 code of the detected language, if identified.
	LanguageCode string `msgpack:"language_code"`
	// AudioMS is the audio position the draft covers up to.
	AudioMS          int `msgpack:"audio_ms"`
	AudioReceivedMS  int `msgpack:"audio_received_ms"`
	AudioProcessedMS int `msgpack:"audio_processed_ms"`
	AudioCommittedMS int `msgpack:"audio_committed_ms"`
}

// ASREagerEndOfTurnEvent says the turn has probably ended, sent when
// TurnDetection.Eager is on. Start preparing a response;
// ASRTurnResumedEvent cancels it.
type ASREagerEndOfTurnEvent struct {
	Seq       int `msgpack:"seq"`
	TurnIndex int `msgpack:"turn_index"`
	// Text is exactly what ASRCommittedEvent will say unless
	// ASRTurnResumedEvent follows. Can be empty.
	Text         string `msgpack:"text"`
	Language     string `msgpack:"language"`
	LanguageCode string `msgpack:"language_code"`
	AudioMS      int    `msgpack:"audio_ms"`
}

// ASRTurnResumedEvent says the speaker went on after an
// ASREagerEndOfTurnEvent: discard that text.
type ASRTurnResumedEvent struct {
	Seq       int `msgpack:"seq"`
	TurnIndex int `msgpack:"turn_index"`
}

// ASRUserBackchannelEvent says the user acknowledged ("mm-hm") without taking
// the turn, while your agent is speaking (auto mode only). Keep talking.
type ASRUserBackchannelEvent struct {
	Seq int `msgpack:"seq"`
	// TurnIndex is the open turn, or the index the next turn will get.
	TurnIndex int `msgpack:"turn_index"`
	// AudioMS is where the acknowledgement is in the audio.
	AudioMS int `msgpack:"audio_ms"`
}

// ASRCommittedEvent is the final transcript of a turn. Every turn has exactly
// one.
type ASRCommittedEvent struct {
	Seq       int `msgpack:"seq"`
	TurnIndex int `msgpack:"turn_index"`
	// Text is the final transcript. It can contain speaker tags such as
	// "<|speaker:0|>" (unless Results.Diarize is false) and sound tags such as
	// "[laughter]" (unless Results.TagAudioEvents is false). Can be empty.
	Text string `msgpack:"text"`
	// Words has word timings when Results.IgnoreTimestamps is false.
	Words        []ASRWord `msgpack:"words"`
	Language     string    `msgpack:"language"`
	LanguageCode string    `msgpack:"language_code"`
	AudioStartMS int       `msgpack:"audio_start_ms"`
	AudioEndMS   int       `msgpack:"audio_end_ms"`
	// Reason is why the turn ended: "eot", "finalize", "silence_timeout",
	// "max_turn_duration" or "audio_done". Treat an unknown value like "eot".
	Reason string `msgpack:"reason"`
	// Degraded is true when the final pass failed and the text comes from the
	// last draft.
	Degraded bool `msgpack:"degraded"`
	// UserTurn is true for the user's turn. Decide whether to answer by this
	// field. It is true when the server leaves it out.
	UserTurn bool `msgpack:"user_turn"`
	// TurnKind is "user", "backchannel" or "other".
	TurnKind         string `msgpack:"turn_kind"`
	AudioReceivedMS  int    `msgpack:"audio_received_ms"`
	AudioProcessedMS int    `msgpack:"audio_processed_ms"`
	AudioCommittedMS int    `msgpack:"audio_committed_ms"`
}

// ASRVADEvent reports voice activity, sent when Results.VADEvents is on.
type ASRVADEvent struct {
	Seq     int `msgpack:"seq"`
	AudioMS int `msgpack:"audio_ms"`
	// Score is the voice activity, from 0 to 1.
	Score float64 `msgpack:"score"`
}

// ASREOTProbEvent reports the end-of-turn likelihood, sent when
// Results.EOTEvents is on.
type ASREOTProbEvent struct {
	Seq       int `msgpack:"seq"`
	TurnIndex int `msgpack:"turn_index"`
	AudioMS   int `msgpack:"audio_ms"`
	// Prob is the likelihood that the turn has ended, from 0 to 1.
	Prob float64 `msgpack:"prob"`
}

// ASRConfiguredEvent is the reply to ASRRealtimeStream.Configure.
type ASRConfiguredEvent struct {
	Seq int `msgpack:"seq"`
	// OK is false when nothing changed; Message says why.
	OK bool `msgpack:"ok"`
	// Config is the configuration in effect.
	Config map[string]interface{} `msgpack:"config"`
	// Message says why the change was refused, in English, for humans.
	Message string `msgpack:"message"`
}

// ASRProgressEvent carries watermarks only, sent every 5 seconds while the
// client sends nothing.
type ASRProgressEvent struct {
	Seq              int `msgpack:"seq"`
	AudioReceivedMS  int `msgpack:"audio_received_ms"`
	AudioProcessedMS int `msgpack:"audio_processed_ms"`
	AudioCommittedMS int `msgpack:"audio_committed_ms"`
}

// ASRErrorEvent is an error the session survived (Recoverable is true), for
// example an audio message that was dropped. An error that ends the session is
// not delivered as an event: it is returned by ASRRealtimeStream.Err as an
// *ASRRealtimeError.
type ASRErrorEvent struct {
	Seq int `msgpack:"seq"`
	// Code is an HTTP-style status.
	Code int `msgpack:"code"`
	// ErrorType identifies the error, such as "invalid_request". Match on
	// this field, not on Message.
	ErrorType string `msgpack:"error_type"`
	// Message is a short English description, for humans.
	Message   string `msgpack:"message"`
	RequestID string `msgpack:"request_id"`
	// Recoverable is true when the session continues. "server_restarting" is
	// recoverable but is followed by close code 1012: reconnect.
	Recoverable bool `msgpack:"recoverable"`
}

// ASRSessionStats summarizes a finished session.
type ASRSessionStats struct {
	// AudioMS is the audio received in the session, in milliseconds.
	AudioMS int `msgpack:"audio_ms"`
	// Turns counts every committed turn, user turn or not.
	Turns     int    `msgpack:"turns"`
	RequestID string `msgpack:"request_id"`
}

// ASRSessionDoneEvent is the last event of a session that ended normally.
type ASRSessionDoneEvent struct {
	Seq   int             `msgpack:"seq"`
	Stats ASRSessionStats `msgpack:"stats"`
}

func (*ASRSessionStartedEvent) EventType() string  { return "session_started" }
func (*ASRSpeechStartEvent) EventType() string     { return "speech_start" }
func (*ASRUserTurnTakenEvent) EventType() string   { return "user_turn_taken" }
func (*ASRDraftEvent) EventType() string           { return "draft" }
func (*ASREagerEndOfTurnEvent) EventType() string  { return "eager_end_of_turn" }
func (*ASRTurnResumedEvent) EventType() string     { return "turn_resumed" }
func (*ASRUserBackchannelEvent) EventType() string { return "user_backchannel" }
func (*ASRCommittedEvent) EventType() string       { return "committed" }
func (*ASRVADEvent) EventType() string             { return "vad" }
func (*ASREOTProbEvent) EventType() string         { return "eot_prob" }
func (*ASRConfiguredEvent) EventType() string      { return "configured" }
func (*ASRProgressEvent) EventType() string        { return "progress" }
func (*ASRErrorEvent) EventType() string           { return "error" }
func (*ASRSessionDoneEvent) EventType() string     { return "session_done" }

// newASREvent returns an empty event for a message type, with the values the
// protocol defines for fields the server leaves out, or nil for a type this
// SDK does not know.
func newASREvent(messageType string) ASREvent {
	switch messageType {
	case "session_started":
		return &ASRSessionStartedEvent{}
	case "speech_start":
		return &ASRSpeechStartEvent{}
	case "user_turn_taken":
		return &ASRUserTurnTakenEvent{}
	case "draft":
		return &ASRDraftEvent{}
	case "eager_end_of_turn":
		return &ASREagerEndOfTurnEvent{}
	case "turn_resumed":
		return &ASRTurnResumedEvent{}
	case "user_backchannel":
		return &ASRUserBackchannelEvent{}
	case "committed":
		return &ASRCommittedEvent{Reason: "eot", UserTurn: true}
	case "vad":
		return &ASRVADEvent{}
	case "eot_prob":
		return &ASREOTProbEvent{}
	case "configured":
		return &ASRConfiguredEvent{}
	case "progress":
		return &ASRProgressEvent{}
	case "error":
		return &ASRErrorEvent{}
	case "session_done":
		return &ASRSessionDoneEvent{}
	default:
		return nil
	}
}

// decodeASREvent parses a server message. It returns nil, nil for a message
// type this SDK does not know: version 1 of the protocol only adds message
// types and fields, so both are skipped.
func decodeASREvent(data []byte) (ASREvent, error) {
	var envelope struct {
		Type string `msgpack:"type"`
	}
	if err := msgpack.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("failed to decode server message: %w", err)
	}
	event := newASREvent(envelope.Type)
	if event == nil {
		return nil, nil
	}
	if err := msgpack.Unmarshal(data, event); err != nil {
		return nil, fmt.Errorf("failed to decode %s message: %w", envelope.Type, err)
	}
	return event, nil
}
