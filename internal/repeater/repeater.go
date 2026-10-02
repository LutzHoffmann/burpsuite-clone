package repeater

import (
	"context"
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
	sender         Sender
	bodyLimitBytes int64
	requestTimeout time.Duration
}

func NewService(transport http.RoundTripper, bodyLimitBytes int64) *Service {
	if bodyLimitBytes < 0 {
		bodyLimitBytes = 0
	}
	return &Service{sender: NewHTTPSender(transport), bodyLimitBytes: bodyLimitBytes, requestTimeout: 60 * time.Second}
}

func (s *Service) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	return s.sender.Send(ctx, req, SendOptions{Timeout: s.requestTimeout, BodyLimitBytes: s.bodyLimitBytes})
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
