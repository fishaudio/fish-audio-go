package fishaudio

import (
	"bytes"
	"io"
	"net/http"
)

// AudioStream wraps an HTTP response for streaming audio data.
//
// It provides two ways to consume audio:
//   - Iterate chunk-by-chunk using Next() and Bytes()
//   - Collect all chunks at once using Collect()
//
// Example:
//
//	// Stream chunks
//	stream, _ := client.TTS.Stream(ctx, params)
//	defer stream.Close()
//	for stream.Next() {
//	    chunk := stream.Bytes()
//	    // process chunk
//	}
//	if err := stream.Err(); err != nil {
//	    // handle error
//	}
//
//	// Or collect all at once
//	stream, _ := client.TTS.Stream(ctx, params)
//	audio, err := stream.Collect()
type AudioStream struct {
	resp      *http.Response
	buf       []byte
	chunkSize int
	err       error
	closed    bool
}

// newAudioStream creates a new AudioStream from an HTTP response.
func newAudioStream(resp *http.Response) *AudioStream {
	return &AudioStream{
		resp:      resp,
		chunkSize: 4096,
	}
}

// Next advances to the next chunk of audio data.
// It returns false when there are no more chunks or an error occurred.
func (s *AudioStream) Next() bool {
	if s.closed || s.err != nil {
		return false
	}

	s.buf = make([]byte, s.chunkSize)
	n, err := s.resp.Body.Read(s.buf)
	if err != nil {
		if err == io.EOF {
			s.closed = true
			return false
		}
		s.err = err
		return false
	}

	s.buf = s.buf[:n]
	return true
}

// Bytes returns the current chunk of audio data.
// Only valid after a successful call to Next().
func (s *AudioStream) Bytes() []byte {
	return s.buf
}

// Err returns any error that occurred during iteration.
func (s *AudioStream) Err() error {
	return s.err
}

// Collect reads all remaining audio data and returns it as a single byte slice.
// This consumes the stream and closes it automatically.
func (s *AudioStream) Collect() ([]byte, error) {
	defer func() { _ = s.Close() }()

	var buf bytes.Buffer
	_, err := io.Copy(&buf, s.resp.Body)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// Close closes the underlying response body.
func (s *AudioStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.resp != nil && s.resp.Body != nil {
		return s.resp.Body.Close()
	}
	return nil
}

// Read implements io.Reader interface.
func (s *AudioStream) Read(p []byte) (n int, err error) {
	if s.closed {
		return 0, io.EOF
	}
	return s.resp.Body.Read(p)
}
