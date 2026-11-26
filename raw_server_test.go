package fishaudio

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// rawField is one header line as it arrived on the wire, before any parsing
// by a Go HTTP server.
type rawField struct{ name, value string }

// rawRequest is one request as it arrived on the wire.
type rawRequest struct {
	line   string // the request line
	fields []rawField
}

// values returns the values of every line whose name equals name, ignoring
// case, in the order they arrived.
func (r rawRequest) values(name string) []string {
	var out []string
	for _, f := range r.fields {
		if strings.EqualFold(f.name, name) {
			out = append(out, f.value)
		}
	}
	return out
}

// rawHeaderServer is a plain TCP server that records each request's header
// block byte for byte, so a test sees what the client wrote rather than what
// a Go server makes of it. It answers HTTP requests with 200 and {} and
// completes WebSocket handshakes (and then closes the connection). It returns
// the base URL and a function that returns the requests recorded so far.
func rawHeaderServer(t *testing.T) (string, func() []rawRequest) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []rawRequest
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close() }()
				br := bufio.NewReader(conn)
				for {
					req, ok := readRawRequest(br)
					if !ok {
						return
					}
					mu.Lock()
					seen = append(seen, req)
					mu.Unlock()
					if strings.Contains(req.line, "/v1/tts/live") {
						key := strings.TrimSpace(strings.Join(req.values("Sec-WebSocket-Key"), ""))
						sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
						_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
							"Sec-WebSocket-Accept: "+base64.StdEncoding.EncodeToString(sum[:])+"\r\n\r\n")
						return
					}
					if n, err := strconv.Atoi(strings.Join(req.values("Content-Length"), "")); err == nil && n > 0 {
						if _, err := io.CopyN(io.Discard, br, int64(n)); err != nil {
							return
						}
					}
					if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"); err != nil {
						return
					}
				}
			}()
		}
	}()
	return "http://" + ln.Addr().String(), func() []rawRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]rawRequest(nil), seen...)
	}
}

// readRawRequest reads a request line and header lines, keeping every byte
// of each value after the colon and the optional whitespace RFC 9112 allows
// around it.
func readRawRequest(br *bufio.Reader) (rawRequest, bool) {
	tp := textproto.NewReader(br)
	line, err := tp.ReadLine()
	if err != nil {
		return rawRequest{}, false
	}
	req := rawRequest{line: line}
	for {
		l, err := tp.ReadLine()
		if err != nil {
			return rawRequest{}, false
		}
		if l == "" {
			return req, true
		}
		name, value, _ := strings.Cut(l, ":")
		req.fields = append(req.fields, rawField{name, strings.Trim(value, " \t")})
	}
}
