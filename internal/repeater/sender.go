package repeater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

type SendOptions struct {
	Timeout        time.Duration
	BodyLimitBytes int64
}

type Sender interface {
	Send(context.Context, SendRequest, SendOptions) (SendResult, error)
}

type httpSender struct {
	transport http.RoundTripper
}

func NewHTTPSender(transport http.RoundTripper) Sender {
	if transport == nil {
		defaultTransport := http.DefaultTransport.(*http.Transport).Clone()
		defaultTransport.Proxy = nil
		defaultTransport.DisableCompression = true
		defaultTransport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		defaultTransport.TLSHandshakeTimeout = 10 * time.Second
		defaultTransport.ResponseHeaderTimeout = 30 * time.Second
		defaultTransport.IdleConnTimeout = 90 * time.Second
		transport = defaultTransport
	}
	return &httpSender{transport: transport}
}

func (s *httpSender) Send(ctx context.Context, req SendRequest, options SendOptions) (SendResult, error) {
	if options.Timeout <= 0 {
		return SendResult{}, errors.New("send timeout must be positive")
	}
	if options.BodyLimitBytes < 0 {
		return SendResult{}, errors.New("response body limit must not be negative")
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return SendResult{}, fmt.Errorf("create request: %w", err)
	}
	if request.URL.User != nil {
		return SendResult{}, errors.New("request URL userinfo is not allowed")
	}
	request.Header = make(http.Header, len(req.Headers))
	for key, values := range req.Headers {
		request.Header[key] = append([]string(nil), values...)
	}

	startedAt := time.Now()
	response, err := s.transport.RoundTrip(request)
	if err != nil {
		return SendResult{}, fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	body, size, truncated, err := readLimitedBody(response.Body, options.BodyLimitBytes)
	if err != nil {
		return SendResult{}, fmt.Errorf("read response: %w", err)
	}
	if response.ContentLength >= 0 {
		size = response.ContentLength
		truncated = size > int64(len(body))
	}
	return SendResult{
		Status: response.StatusCode, Headers: response.Header.Clone(), Body: body,
		DurationMS: time.Since(startedAt).Milliseconds(), Size: size, Truncated: truncated,
		ContentType: response.Header.Get("Content-Type"),
	}, nil
}
