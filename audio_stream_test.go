package fishaudio

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

// mockReadCloser is a simple mock for http.Response.Body
type mockReadCloser struct {
	*bytes.Reader
	closed bool
}

func newMockReadCloser(data []byte) *mockReadCloser {
	return &mockReadCloser{Reader: bytes.NewReader(data)}
}

func (m *mockReadCloser) Close() error {
	m.closed = true
	return nil
}

func TestAudioStream_Collect(t *testing.T) {
	data := []byte("audio data for collection test")
	resp := &http.Response{
		Body: newMockReadCloser(data),
	}
	stream := newAudioStream(resp)

	collected, err := stream.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !bytes.Equal(collected, data) {
		t.Errorf("Collect() = %q, want %q", string(collected), string(data))
	}

	// Verify stream is closed
	if !stream.closed {
		t.Error("stream should be closed after Collect()")
	}
}

func TestAudioStream_Next_And_Bytes(t *testing.T) {
	data := []byte("chunk1chunk2chunk3")
	resp := &http.Response{
		Body: newMockReadCloser(data),
	}
	stream := newAudioStream(resp)
	stream.chunkSize = 6 // smaller chunks for testing

	var collected bytes.Buffer
	for stream.Next() {
		collected.Write(stream.Bytes())
	}

	if stream.Err() != nil {
		t.Fatalf("Err() = %v, want nil", stream.Err())
	}

	if !bytes.Equal(collected.Bytes(), data) {
		t.Errorf("collected = %q, want %q", collected.String(), string(data))
	}
}

func TestAudioStream_Next_Empty(t *testing.T) {
	resp := &http.Response{
		Body: newMockReadCloser([]byte{}),
	}
	stream := newAudioStream(resp)

	if stream.Next() {
		t.Error("Next() should return false for empty stream")
	}
	if stream.Err() != nil {
		t.Errorf("Err() = %v, want nil", stream.Err())
	}
}

func TestAudioStream_Close(t *testing.T) {
	mockBody := newMockReadCloser([]byte("data"))
	resp := &http.Response{
		Body: mockBody,
	}
	stream := newAudioStream(resp)

	err := stream.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if !mockBody.closed {
		t.Error("body should be closed after Close()")
	}
	if !stream.closed {
		t.Error("stream.closed should be true")
	}

	// Double close should not error
	err = stream.Close()
	if err != nil {
		t.Errorf("double Close() error = %v", err)
	}
}

func TestAudioStream_Close_NilBody(t *testing.T) {
	stream := &AudioStream{resp: nil}
	err := stream.Close()
	if err != nil {
		t.Errorf("Close() with nil resp error = %v", err)
	}

	stream2 := &AudioStream{resp: &http.Response{Body: nil}}
	err = stream2.Close()
	if err != nil {
		t.Errorf("Close() with nil body error = %v", err)
	}
}

func TestAudioStream_Read(t *testing.T) {
	data := []byte("audio data for read test")
	resp := &http.Response{
		Body: newMockReadCloser(data),
	}
	stream := newAudioStream(resp)

	buf := make([]byte, 10)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 10 {
		t.Errorf("Read() n = %d, want %d", n, 10)
	}
	if string(buf) != "audio data" {
		t.Errorf("Read() buf = %q, want %q", string(buf), "audio data")
	}
}

func TestAudioStream_Read_Closed(t *testing.T) {
	data := []byte("data")
	resp := &http.Response{
		Body: newMockReadCloser(data),
	}
	stream := newAudioStream(resp)
	_ = stream.Close()

	buf := make([]byte, 10)
	n, err := stream.Read(buf)
	if n != 0 {
		t.Errorf("Read() on closed stream n = %d, want 0", n)
	}
	if err != io.EOF {
		t.Errorf("Read() on closed stream err = %v, want io.EOF", err)
	}
}

func TestAudioStream_Read_Full(t *testing.T) {
	data := []byte("complete audio data")
	resp := &http.Response{
		Body: newMockReadCloser(data),
	}
	stream := newAudioStream(resp)

	// Read all using io.ReadAll
	collected, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}

	if !bytes.Equal(collected, data) {
		t.Errorf("io.ReadAll() = %q, want %q", string(collected), string(data))
	}
}

func TestAudioStream_Err_NoError(t *testing.T) {
	stream := &AudioStream{}
	if stream.Err() != nil {
		t.Errorf("Err() = %v, want nil", stream.Err())
	}
}

func TestAudioStream_Bytes_Empty(t *testing.T) {
	stream := &AudioStream{}
	if stream.Bytes() != nil {
		t.Errorf("Bytes() = %v, want nil", stream.Bytes())
	}
}

func TestNewAudioStream(t *testing.T) {
	resp := &http.Response{
		Body: newMockReadCloser([]byte("data")),
	}
	stream := newAudioStream(resp)

	if stream.resp != resp {
		t.Error("resp not set correctly")
	}
	if stream.chunkSize != 4096 {
		t.Errorf("chunkSize = %d, want %d", stream.chunkSize, 4096)
	}
	if stream.closed {
		t.Error("closed should be false")
	}
	if stream.err != nil {
		t.Error("err should be nil")
	}
}

func TestAudioStream_Next_AfterClosed(t *testing.T) {
	data := []byte("data")
	resp := &http.Response{
		Body: newMockReadCloser(data),
	}
	stream := newAudioStream(resp)
	_ = stream.Close()

	if stream.Next() {
		t.Error("Next() should return false after Close()")
	}
}

func TestAudioStream_Next_AfterError(t *testing.T) {
	stream := &AudioStream{
		err: io.ErrUnexpectedEOF,
	}

	if stream.Next() {
		t.Error("Next() should return false when err is set")
	}
}
