package repeater

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
		transport = http.DefaultTransport
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
	var captured bytes.Buffer
	buffer := make([]byte, 32*1024)
	var size int64
	for {
		n, err := body.Read(buffer)
		if n > 0 {
			size += int64(n)
			remaining := limit - int64(captured.Len())
			if remaining > 0 {
				if int64(n) < remaining {
					remaining = int64(n)
				}
				_, _ = captured.Write(buffer[:remaining])
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, false, err
		}
	}
	return captured.Bytes(), size, size > limit, nil
}
