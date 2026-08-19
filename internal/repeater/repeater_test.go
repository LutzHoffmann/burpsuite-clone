package repeater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendReturnsCapturedResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			t.Fatalf("X-Test = %q", r.Header.Get("X-Test"))
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("repeater response"))
	}))
	defer target.Close()

	service := NewService(http.DefaultTransport, 2048)
	result, err := service.Send(context.Background(), SendRequest{
		Method: "POST",
		URL:    target.URL + "/repeat",
		Headers: map[string][]string{
			"X-Test": {"yes"},
		},
		Body: []byte("request"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 200 {
		t.Fatalf("Status = %d", result.Status)
	}
	if string(result.Body) != "repeater response" {
		t.Fatalf("Body = %q", result.Body)
	}
}

func TestSendCapturesTruncatedResponseMetadata(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Response-Test", "yes")
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer target.Close()

	result, err := NewService(http.DefaultTransport, 4).Send(context.Background(), SendRequest{
		Method: http.MethodGet,
		URL:    target.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Body) != "abcd" {
		t.Fatalf("body = %q", result.Body)
	}
	if result.Size != 6 {
		t.Fatalf("size = %d", result.Size)
	}
	if !result.Truncated {
		t.Fatal("expected truncated response")
	}
	if result.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", result.ContentType)
	}
	if result.Headers["X-Response-Test"][0] != "yes" {
		t.Fatalf("response headers = %#v", result.Headers)
	}
}

func TestReadLimitedBodyPreservesBytesReturnedWithReadError(t *testing.T) {
	readErr := errors.New("read failed")
	body, size, truncated, err := readLimitedBody(&bytesThenErrorReader{data: []byte("response"), err: readErr}, 16)
	if !errors.Is(err, readErr) {
		t.Fatalf("error = %v", err)
	}
	if string(body) != "response" {
		t.Fatalf("body = %q", body)
	}
	if size != int64(len("response")) {
		t.Fatalf("size = %d", size)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
}

func TestReadLimitedBodyAcceptsBytesReturnedWithEOF(t *testing.T) {
	body, size, truncated, err := readLimitedBody(&bytesThenErrorReader{data: []byte("response"), err: io.EOF}, 16)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "response" {
		t.Fatalf("body = %q", body)
	}
	if size != int64(len("response")) || truncated {
		t.Fatalf("size = %d, truncated = %t", size, truncated)
	}
}

type bytesThenErrorReader struct {
	data []byte
	err  error
}

func (r *bytesThenErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

var _ io.Reader = (*bytesThenErrorReader)(nil)
