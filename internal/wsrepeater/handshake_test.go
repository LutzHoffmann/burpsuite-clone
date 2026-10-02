package wsrepeater

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestHandshakeTransportCloseDoesNotWaitForTLSCloseNotify(t *testing.T) {
	fixture := httptest.NewTLSServer(nil)
	defer fixture.Close()
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	server := tls.Server(right, &tls.Config{Certificates: fixture.TLS.Certificates, MaxVersion: tls.VersionTLS12})
	client := tls.Client(left, &tls.Config{RootCAs: roots, ServerName: "example.com", MaxVersion: tls.VersionTLS12})
	handshaken := make(chan error, 1)
	go func() { handshaken <- server.Handshake() }()
	if e := client.Handshake(); e != nil {
		t.Fatal(e)
	}
	if e := <-handshaken; e != nil {
		t.Fatal(e)
	}
	bounded := &handshakeConn{Conn: client, done: true}
	closed := make(chan struct{})
	go func() { _ = bounded.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(100 * time.Millisecond):
		_ = left.Close()
		<-closed
		t.Fatal("forced close waited for TLS close notification")
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
