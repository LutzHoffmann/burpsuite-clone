package repeater

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPSenderUsesExactRequestAndBoundsResponse(t *testing.T) {
	sender := NewHTTPSender(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Method != "PATCH" || request.URL.String() != "https://example.test/path" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		if string(body) != "payload" || request.Header.Get("X-Test") != "yes" {
			t.Fatalf("body/header = %q / %q", body, request.Header.Get("X-Test"))
		}
		return &http.Response{
			StatusCode:    201,
			Header:        http.Header{"Content-Type": {"text/plain"}},
			Body:          io.NopCloser(strings.NewReader("abcdef")),
			ContentLength: 6,
		}, nil
	}))
	result, err := sender.Send(context.Background(), SendRequest{
		Method: "PATCH", URL: "https://example.test/path",
		Headers: map[string][]string{"X-Test": {"yes"}}, Body: []byte("payload"),
	}, SendOptions{Timeout: time.Second, BodyLimitBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 201 || !bytes.Equal(result.Body, []byte("abcd")) || result.Size != 6 || !result.Truncated {
		t.Fatalf("result = %+v", result)
	}
}

func TestHTTPSenderAppliesOptionDeadlineAndCancellation(t *testing.T) {
	calls := 0
	sender := NewHTTPSender(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			deadline, ok := request.Context().Deadline()
			if !ok || time.Until(deadline) > 110*time.Millisecond {
				t.Fatalf("deadline = %v, %t", deadline, ok)
			}
		}
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	_, err := sender.Send(context.Background(), SendRequest{Method: "GET", URL: "http://example.test"}, SendOptions{Timeout: 100 * time.Millisecond, BodyLimitBytes: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = sender.Send(ctx, SendRequest{Method: "GET", URL: "http://example.test"}, SendOptions{Timeout: time.Second, BodyLimitBytes: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestDefaultHTTPSenderDisablesEnvironmentProxyAndCompression(t *testing.T) {
	sender := NewHTTPSender(nil)
	implementation, ok := sender.(*httpSender)
	if !ok {
		t.Fatalf("sender type = %T", sender)
	}
	transport, ok := implementation.transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", implementation.transport)
	}
	if transport.Proxy != nil {
		t.Fatal("default sender discovers environment proxy")
	}
	if !transport.DisableCompression {
		t.Fatal("default sender allows implicit decompression")
	}
}

func TestHTTPSenderRejectsInvalidOptions(t *testing.T) {
	sender := NewHTTPSender(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport called")
		return nil, nil
	}))
	for _, options := range []SendOptions{{}, {Timeout: time.Second, BodyLimitBytes: -1}} {
		if _, err := sender.Send(context.Background(), SendRequest{Method: "GET", URL: "http://example.test"}, options); err == nil {
			t.Fatalf("options %+v accepted", options)
		}
	}
}
