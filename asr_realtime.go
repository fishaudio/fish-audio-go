package fishaudio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	asrRealtimePath = "/v1/asr/realtime"

	// asrEventBuffer is how many events a session holds for a consumer that
	// reads slowly. When it is full the session stops reading from the
	// socket, so the server sees backpressure instead of the SDK buffering
	// without limit.
	asrEventBuffer = 256
)

// These are variables so that tests can shorten them.
var (
	// asrCloseGrace is how long a session waits for the server's close frame
	// after an error that ends the session, and for the reason of a broken
	// connection after a failed send.
	asrCloseGrace = 2 * time.Second
	// asrCloseFrameTimeout bounds the write of the close frame.
	asrCloseFrameTimeout = 250 * time.Millisecond
)

// StreamWebSocket opens a realtime speech-to-text session and streams audio to
// it.
//
// Send 16-bit little-endian mono PCM on audio, 20-40 ms per chunk for live
// audio, at params.SampleRate. Close the channel when there is no more audio:
// the server commits the open turn, sends ASRSessionDoneEvent, and the stream
// ends. StreamWebSocket returns once the server has started the session, so a
// refused connection (*AuthenticationError, *RateLimitError, ...) or an
// invalid configuration (*ASRRealtimeError) is returned here.
//
// Read the returned stream with Next, Event and Err:
//
//	stream, err := client.ASR.StreamWebSocket(ctx, audio, &fishaudio.ASRRealtimeParams{
//	    Results: &fishaudio.ASRResults{Drafts: fishaudio.Bool(true), Diarize: fishaudio.Bool(false)},
//	}, nil)
//	if err != nil {
//	    return err
//	}
//	defer stream.Close()
//	for stream.Next() {
//	    switch e := stream.Event().(type) {
//	    case *fishaudio.ASRDraftEvent:
//	        fmt.Println("...", e.Text)
//	    case *fishaudio.ASRCommittedEvent:
//	        fmt.Println(e.Text)
//	    }
//	}
//	return stream.Err()
//
// Transcripts start with a speaker tag such as "<|speaker:0|>" unless
// params.Results.Diarize is Bool(false).
//
// Canceling ctx, or reaching its deadline, ends the session at once, at any
// point from the dial on. The error, returned here or by Err, matches
// ctx.Err() with errors.Is, and context.Cause(ctx) too: to end a session
// because the audio source failed, cancel ctx with that error
// (context.WithCancelCause).
//
// With the default options the SDK sends a WebSocket ping every 20 seconds,
// which the server counts as activity: a session stays open, and billed, while
// the audio channel is idle. Close the channel or cancel ctx when your audio
// source stops, or set opts.PingInterval to 0 to let the server close a
// session after 30 idle seconds.
func (s *ASRService) StreamWebSocket(ctx context.Context, audio <-chan []byte, params *ASRRealtimeParams, opts *WebSocketOptions) (*ASRRealtimeStream, error) {
	if audio == nil {
		return nil, errors.New("fishaudio: audio channel is nil")
	}
	if opts == nil {
		opts = DefaultWebSocketOptions()
	}
	if params == nil {
		params = &ASRRealtimeParams{}
	}

	config, err := msgpack.Marshal(newASRConfigMessage(params))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	wsURL := strings.Replace(strings.Replace(s.client.baseURL, "https://", "wss://", 1), "http://", "ws://", 1) + asrRealtimePath

	model := params.Model
	if model == "" {
		model = ASRModelTranscribeStream1
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+s.client.apiKey)
	header.Set("User-Agent", "fish-audio/go/"+Version)
	header.Set("model", string(model))

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
		ReadBufferSize:   opts.ReadBufferSize,
		WriteBufferSize:  opts.WriteBufferSize,
	}
	// The dialer applies ctx's deadline to the upgrade but does not watch for
	// cancellation once the TCP connection is up: close that connection when
	// ctx is done, so that a stalled upgrade ends with ctx too.
	var (
		dialMu  sync.Mutex
		dialing net.Conn
	)
	dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		netConn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil {
			dialMu.Lock()
			dialing = netConn
			dialMu.Unlock()
		}
		return netConn, err
	}
	stopWatchingDial := context.AfterFunc(ctx, func() {
		dialMu.Lock()
		defer dialMu.Unlock()
		if dialing != nil {
			_ = dialing.Close()
		}
	})
	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	stopWatchingDial()
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) && resp != nil {
			// The upgrade was refused (401, 402, 429, ...): same errors as the HTTP API.
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return nil, newAPIError(resp.StatusCode, resp.Status, string(body))
		}
		if ctxErr := asrContextError(ctx); ctxErr != nil && !errors.Is(err, ctx.Err()) {
			// The dialer reports a canceled upgrade as the I/O error of the
			// connection it closed: keep ctx's error reachable.
			err = fmt.Errorf("%w: %w", ctxErr, err)
		}
		return nil, &WebSocketError{Message: "websocket dial failed: " + err.Error(), Err: err}
	}
	conn.SetReadLimit(opts.MaxMessageSize)

	stream := &ASRRealtimeStream{
		conn:    conn,
		events:  make(chan ASREvent, asrEventBuffer),
		control: make(chan asrControl),
		started: make(chan struct{}),
		done:    make(chan struct{}),
		aborted: make(chan struct{}),
	}

	// From here on ctx is watched on its own, not by whichever goroutine is
	// writing: the dial deadline is gone once the upgrade has succeeded, and a
	// write (the config below, audio later) can be stuck on a peer that
	// stopped reading. Aborting closes the connection, which releases it.
	stopWatchingCtx := context.AfterFunc(ctx, func() {
		stream.abort(asrContextError(ctx))
	})
	stream.mu.Lock()
	stream.stopWatchingCtx = stopWatchingCtx
	stream.mu.Unlock()

	if err := conn.WriteMessage(websocket.BinaryMessage, config); err != nil {
		// If ctx is done, that is the error: this one is only the write
		// failing on the connection that was closed for it.
		if ctxErr := asrContextError(ctx); ctxErr != nil {
			stream.abort(ctxErr)
		} else {
			stream.abort(&WebSocketError{Message: "failed to send config: " + err.Error(), Err: err})
		}
		return nil, stream.Err()
	}

	stream.wg.Add(1)
	go stream.readLoop()

	// Audio may only be sent after session_started.
	select {
	case <-stream.started:
	case <-stream.done:
		stream.wg.Wait()
		if err := stream.Err(); err != nil {
			return nil, err
		}
		return nil, &WebSocketError{Message: "websocket closed before the session started"}
	}

	stream.wg.Add(1)
	go stream.sendLoop(audio, opts.PingInterval)

	return stream, nil
}

// ASRRealtimeStream is a realtime speech-to-text session.
//
// Iterate it with Next, Event and Err from one goroutine. The control methods
// (Finalize, AgentSpeaking, AddContext, ReplaceContext, Configure, Keepalive)
// and Close are safe to call from any goroutine.
//
// A control message applies at its position in the audio stream: it is sent
// after every chunk the session has already taken from the audio channel, and
// before the chunks still waiting in it. With an unbuffered channel, a chunk
// is taken when your send on the channel returns, so Finalize called after
// that send follows the chunk.
//
// The stream ends when the server sends ASRSessionDoneEvent (after the audio
// channel is closed), when the session fails, when ctx is canceled, or when
// Close is called. The connection is closed by then, whichever it was.
type ASRRealtimeStream struct {
	conn *websocket.Conn
	// control hands control messages to sendLoop, the only writer of data
	// frames, so that they keep their place among the audio chunks.
	control chan asrControl

	// events is written and closed by readLoop only.
	events chan ASREvent
	// started is closed when session_started arrives.
	started chan struct{}
	// done is closed when the session has ended, for any reason. The
	// connection is closed and err is final by then.
	done     chan struct{}
	doneOnce sync.Once
	// aborted is closed when the session is cut short locally (Close, ctx,
	// failed send): events still buffered are dropped rather than delivered.
	aborted   chan struct{}
	abortOnce sync.Once

	wg sync.WaitGroup

	mu sync.Mutex
	// stopWatchingCtx releases the watch on ctx once the session has ended.
	// It is nil if ctx was canceled before the watch was recorded.
	stopWatchingCtx func() bool
	err             error
	current         ASREvent
}

// Next advances to the next event. It returns false when the session has
// ended; check Err to see whether it ended with an error.
//
// Events the server sent before a normal end or a server-side failure are all
// delivered first. After Close, a canceled ctx or a failed send, Next returns
// false at once.
func (s *ASRRealtimeStream) Next() bool {
	select {
	case <-s.aborted:
		return false
	default:
	}
	select {
	case event, ok := <-s.events:
		if !ok {
			return false
		}
		s.mu.Lock()
		s.current = event
		s.mu.Unlock()
		return true
	case <-s.aborted:
		return false
	}
}

// Event returns the event Next advanced to.
func (s *ASRRealtimeStream) Event() ASREvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Err returns the error that ended the session, or nil if it ended normally,
// has not ended, or was ended by Close.
//
// It is an *ASRRealtimeError when the server ended the session with an error
// or closed the connection with a code other than 1000, a *WebSocketError
// when the connection was lost, and an error matching ctx.Err() and
// context.Cause(ctx) (with errors.Is) when ctx ended the session.
func (s *ASRRealtimeStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close ends the session now and closes the connection, without waiting for
// the open turn to be committed. A goroutine blocked in Next returns false.
// It is safe to call more than once and after the session has ended.
//
// To end a session normally instead, close the audio channel and keep reading
// until Next returns false.
func (s *ASRRealtimeStream) Close() error {
	s.abort(nil)
	s.wg.Wait()
	return nil
}

// Finalize commits the current turn now. Use it for push-to-talk
// (ASRTurnDetectionManual) when the button is released.
func (s *ASRRealtimeStream) Finalize() error {
	return s.send(&asrSimpleMessage{Type: "finalize"})
}

// AgentSpeaking tells the server that your voice agent started or stopped
// speaking, so that it can tell an acknowledgement ("mm-hm",
// ASRUserBackchannelEvent) from an interruption (ASRUserTurnTakenEvent). Send
// it only when the value changes.
func (s *ASRRealtimeStream) AgentSpeaking(speaking bool) error {
	return s.send(&asrAgentSpeakingMessage{Type: "agent_speaking", Speaking: speaking})
}

// AddContext adds conversation context, oldest first, up to 32 items:
// typically each line your agent speaks, as an ASRContextRoleAssistant item,
// right after the ASRCommittedEvent it answers. Committed user turns are added
// by the server.
func (s *ASRRealtimeStream) AddContext(items ...ASRContextItem) error {
	return s.send(&asrContextAddMessage{Type: "context_add", Items: append([]ASRContextItem{}, items...)})
}

// ReplaceContext drops the items added before with AddContext or
// ReplaceContext, then adds items.
func (s *ASRRealtimeStream) ReplaceContext(items ...ASRContextItem) error {
	return s.send(&asrContextAddMessage{Type: "context_add", Items: append([]ASRContextItem{}, items...), ResetHistory: true})
}

// Configure changes settings during the session. The server answers with an
// ASRConfiguredEvent; if any value is invalid, nothing is applied and its OK
// is false.
func (s *ASRRealtimeStream) Configure(params *ASRConfigureParams) error {
	if params == nil {
		params = &ASRConfigureParams{}
	}
	return s.send(newASRConfigureMessage(params))
}

// Keepalive keeps an idle session open. It is rarely needed: the WebSocket
// pings the SDK sends (WebSocketOptions.PingInterval) count as activity too.
func (s *ASRRealtimeStream) Keepalive() error {
	return s.send(&asrSimpleMessage{Type: "keepalive"})
}

// asrControl is a control message on its way to sendLoop.
type asrControl struct {
	data   []byte
	result chan error // buffered: sendLoop never waits for the caller
}

var errASRSessionClosed = &WebSocketError{Message: "realtime session is closed"}

// send queues one control message behind the audio already taken from the
// channel, and waits until it is written.
func (s *ASRRealtimeStream) send(message interface{}) error {
	data, err := msgpack.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	select {
	case <-s.done:
		return errASRSessionClosed
	default:
	}
	request := asrControl{data: data, result: make(chan error, 1)}
	select {
	case s.control <- request:
	case <-s.done:
		return errASRSessionClosed
	}
	select {
	case err := <-request.result:
		return err
	case <-s.done:
		// sendLoop may have written it just before the session ended.
		select {
		case err := <-request.result:
			return err
		default:
			return errASRSessionClosed
		}
	}
}

// write sends one data frame. Only sendLoop calls it.
func (s *ASRRealtimeStream) write(message interface{}) error {
	data, err := msgpack.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	return s.writeFrame(data)
}

func (s *ASRRealtimeStream) writeFrame(data []byte) error {
	if err := s.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		return &WebSocketError{Message: "failed to send: " + err.Error(), Err: err}
	}
	return nil
}

// finish ends the session with err (nil for a normal end) and closes the
// connection. Only the first call has an effect.
func (s *ASRRealtimeStream) finish(err error) {
	s.doneOnce.Do(func() {
		s.mu.Lock()
		s.err = err
		stopWatchingCtx := s.stopWatchingCtx
		s.mu.Unlock()
		close(s.done)
		if stopWatchingCtx != nil {
			stopWatchingCtx()
		}
		// Best effort, and bounded: the peer may have stopped reading.
		_ = s.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(asrCloseFrameTimeout))
		// Unblocks a ReadMessage or WriteMessage in progress.
		_ = s.conn.Close()
	})
}

// abort ends the session from this side and drops events not yet delivered,
// so that a consumer in Next, and a readLoop waiting for room in the buffer,
// are released at once.
func (s *ASRRealtimeStream) abort(err error) {
	s.finish(err)
	s.abortOnce.Do(func() { close(s.aborted) })
}

// readLoop turns server messages into events until the session ends.
func (s *ASRRealtimeStream) readLoop() {
	defer s.wg.Done()
	defer close(s.events)

	var (
		lastError *ASRErrorEvent // the error message a close may belong to
		fatal     bool           // lastError ends the session
	)
	for {
		messageType, data, err := s.conn.ReadMessage()
		if err != nil {
			s.finish(asrCloseError(err, lastError, fatal))
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		event, err := decodeASREvent(data)
		if err != nil {
			s.finish(&WebSocketError{Message: err.Error(), Err: err})
			return
		}
		if event == nil {
			continue // a message type this SDK does not know
		}
		if fatal {
			continue // nothing after a fatal error is part of the session
		}

		switch e := event.(type) {
		case *ASRErrorEvent:
			lastError = e
			if !e.Recoverable {
				// The session is over. Wait briefly for the close frame, to
				// report its code along with the error.
				fatal = true
				_ = s.conn.SetReadDeadline(time.Now().Add(asrCloseGrace))
				continue
			}
		case *ASRSessionDoneEvent:
			// Close before handing out the last event, so that a caller who
			// stops at session_done does not leave the connection open.
			s.finish(nil)
		case *ASRSessionStartedEvent:
			select {
			case <-s.started:
			default:
				close(s.started)
			}
		default:
			// server_restarting is recoverable but is followed by close 1012,
			// after the open turn is committed.
			if lastError != nil && lastError.ErrorType != "server_restarting" {
				lastError = nil
			}
		}

		// Blocks while the buffer is full: backpressure on the socket.
		select {
		case s.events <- event:
		case <-s.aborted:
			return
		}
		if _, ok := event.(*ASRSessionDoneEvent); ok {
			return
		}
	}
}

// asrContextError returns the error of a ctx that is done, or nil. It matches
// both ctx.Err() and context.Cause(ctx) with errors.Is, so that a caller can
// test for context.Canceled whether or not a cause was given.
func asrContextError(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, err) {
		return cause
	}
	return fmt.Errorf("%w: %w", err, cause)
}

// asrCloseError turns the error that ended reading into the session's error:
// nil for a normal close.
func asrCloseError(err error, lastError *ASRErrorEvent, fatal bool) error {
	var closeErr *websocket.CloseError
	isClose := errors.As(err, &closeErr)
	if isClose && closeErr.Code == websocket.CloseNormalClosure && !fatal {
		return nil
	}
	if !isClose && !fatal {
		return &WebSocketError{Message: "websocket connection lost: " + err.Error(), Err: err}
	}

	result := &ASRRealtimeError{}
	if isClose {
		result.CloseCode = closeErr.Code
		result.Message = closeErr.Text
	}
	if lastError != nil {
		result.ErrorType = lastError.ErrorType
		result.Code = lastError.Code
		result.RequestID = lastError.RequestID
		if lastError.Message != "" {
			result.Message = lastError.Message
		}
	}
	if result.Message == "" {
		result.Message = "websocket closed unexpectedly"
	}
	return result
}

// sendLoop writes every data frame of the session: audio until the channel is
// closed, and control messages. It also sends pings, until the session ends.
// It does not watch ctx: it can be stuck in a write when ctx is canceled.
func (s *ASRRealtimeStream) sendLoop(audio <-chan []byte, pingInterval time.Duration) {
	defer s.wg.Done()

	var ping <-chan time.Time
	if pingInterval > 0 {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		ping = ticker.C
	}

	for {
		select {
		case <-s.done:
			return
		case <-ping:
			// Fails only on a broken connection, which readLoop reports.
			_ = s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(pingInterval))
		case request := <-s.control:
			// A failed write is the caller's to see; if the connection is
			// broken, readLoop ends the session.
			request.result <- s.writeFrame(request.data)
		case chunk, ok := <-audio:
			var err error
			switch {
			case !ok:
				audio = nil // a nil channel is never ready: audio is over
				err = s.write(&asrSimpleMessage{Type: "audio_done"})
			case len(chunk) > 0:
				err = s.write(&asrAudioMessage{Type: "audio", Data: chunk})
			}
			if err != nil {
				s.sendFailed(err)
				return
			}
		}
	}
}

// sendFailed ends the session after a failed write. A write usually fails
// because the server closed the connection, and readLoop then knows why: give
// it a moment to report that before reporting the write error itself.
func (s *ASRRealtimeStream) sendFailed(err error) {
	timer := time.NewTimer(asrCloseGrace)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-timer.C:
		s.abort(err)
	}
}
