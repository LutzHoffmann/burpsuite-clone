package wsrepeater

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

func TestHandshakeBoundPreservesBufferedAndSubsequentBytes(t *testing.T) {
	for _, ending := range []string{"\r\n\r\n", "\n\n"} {
		for _, readSize := range []int{1, 4096} {
			t.Run(fmt.Sprintf("ending=%q/read=%d", ending, readSize), func(t *testing.T) {
				left, right := net.Pipe()
				defer left.Close()
				header := "HTTP/1.1 101 Switching Protocols\r\nX: " + strings.Repeat("a", maxHandshakeBytes-len("HTTP/1.1 101 Switching Protocols\r\nX: ")-len(ending)) + ending
				want := header + strings.Repeat("frame bytes", 10000)
				go func() { defer right.Close(); _, _ = io.WriteString(right, want) }()
				bounded := &handshakeConn{Conn: left, remaining: maxHandshakeBytes}
				var got bytes.Buffer
				_, err := io.CopyBuffer(struct{ io.Writer }{&got}, struct{ io.Reader }{bounded}, make([]byte, readSize))
				if err != nil || got.String() != want {
					t.Fatalf("stream changed: length=%d error=%v", got.Len(), err)
				}
			})
		}
	}
}

func TestHandshakeBoundRejectsUnterminatedStream(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	go func() { defer right.Close(); _, _ = io.WriteString(right, strings.Repeat("a", maxHandshakeBytes+1)) }()
	bounded := &handshakeConn{Conn: left, remaining: maxHandshakeBytes}
	n, err := io.Copy(io.Discard, bounded)
	if n != maxHandshakeBytes || !errors.Is(err, errHandshakeTooLarge) {
		t.Fatalf("read %d bytes: %v", n, err)
	}
}
