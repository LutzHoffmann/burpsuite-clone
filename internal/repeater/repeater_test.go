package repeater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSendAppliesSixtySecondDeadline(t *testing.T) {
	service := NewService(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("request has no total deadline")
		} else if remaining := time.Until(deadline); remaining <= 59*time.Second || remaining > 60*time.Second {
			t.Errorf("remaining deadline = %v", remaining)
		}
		return nil, context.Canceled
	}), 1024)
	_, _ = service.Send(context.Background(), SendRequest{Method: http.MethodGet, URL: "http://example.test"})
}

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

func TestSendCancelsStalledResponse(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		name := "headers"
		if flushHeaders {
			name = "body"
		}
		t.Run(name, func(t *testing.T) {
			disconnected := make(chan struct{})
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if flushHeaders {
					_, _ = io.WriteString(w, "partial")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(disconnected)
			}))
			defer target.Close()
			service := NewService(nil, 1024)
			service.requestTimeout = 100 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := service.Send(ctx, SendRequest{Method: http.MethodGet, URL: target.URL})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("parent timeout fired instead of service deadline")
			}
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("upstream connection was not cancelled")
			}
		})
	}
}

func TestSendPreservesEarlierCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	service := NewService(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if got, _ := r.Context().Deadline(); !got.Equal(want) {
			t.Errorf("deadline = %v, want %v", got, want)
		}
		return nil, context.Canceled
	}), 1024)
	_, err := service.Send(ctx, SendRequest{Method: http.MethodGet, URL: "http://example.test"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
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

func TestReadLimitedBodyStopsAfterDetectingTruncation(t *testing.T) {
	reader := &countingInfiniteReader{}
	body, size, truncated, err := readLimitedBody(reader, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "xxxx" || size != 5 || !truncated {
		t.Fatalf("body = %q, size = %d, truncated = %t", body, size, truncated)
	}
	if reader.reads > 1 {
		t.Fatalf("reader calls = %d, want at most 1", reader.reads)
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

type countingInfiniteReader struct {
	reads int
}

func (r *countingInfiniteReader) Read(p []byte) (int, error) {
	r.reads++
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
