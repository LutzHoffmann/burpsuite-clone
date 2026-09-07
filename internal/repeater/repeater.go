package repeater

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type SendRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type SendResult struct {
	Status      int                 `json:"status"`
	Headers     map[string][]string `json:"headers"`
	Body        []byte              `json:"body"`
	DurationMS  int64               `json:"durationMs"`
	Size        int64               `json:"size"`
	Truncated   bool                `json:"truncated"`
	ContentType string              `json:"contentType"`
}

type Service struct {
	transport      http.RoundTripper
	bodyLimitBytes int64
}

func NewService(transport http.RoundTripper, bodyLimitBytes int64) *Service {
	if transport == nil {
		defaultTransport := http.DefaultTransport.(*http.Transport).Clone()
		defaultTransport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		defaultTransport.TLSHandshakeTimeout = 10 * time.Second
		defaultTransport.ResponseHeaderTimeout = 30 * time.Second
		defaultTransport.IdleConnTimeout = 90 * time.Second
		transport = defaultTransport
	}
	if bodyLimitBytes < 0 {
		bodyLimitBytes = 0
	}
	return &Service{transport: transport, bodyLimitBytes: bodyLimitBytes}
}

func (s *Service) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	request, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return SendResult{}, fmt.Errorf("create repeater request: %w", err)
	}
	request.Header = make(http.Header, len(req.Headers))
	for key, values := range req.Headers {
		request.Header[key] = append([]string(nil), values...)
	}

	startedAt := time.Now()
	response, err := s.transport.RoundTrip(request)
	if err != nil {
		return SendResult{}, fmt.Errorf("send repeater request: %w", err)
	}
	defer response.Body.Close()

	body, size, truncated, err := readLimitedBody(response.Body, s.bodyLimitBytes)
	if err != nil {
		return SendResult{}, fmt.Errorf("read repeater response: %w", err)
	}
	if response.ContentLength >= 0 {
		size = response.ContentLength
		truncated = size > int64(len(body))
	}
	return SendResult{
		Status:      response.StatusCode,
		Headers:     response.Header.Clone(),
		Body:        body,
		DurationMS:  time.Since(startedAt).Milliseconds(),
		Size:        size,
		Truncated:   truncated,
		ContentType: response.Header.Get("Content-Type"),
	}, nil
}

func readLimitedBody(body io.Reader, limit int64) ([]byte, int64, bool, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	size := int64(len(data))
	truncated := size > limit
	if truncated {
		data = data[:limit]
	}
	return data, size, truncated, err
}
