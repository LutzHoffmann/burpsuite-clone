package proxy

import (
	"bytes"
	"io"
	"net/http"
)

func readLimitedBody(body io.ReadCloser, limit int64) ([]byte, bool, error) {
	defer body.Close()
	var buf bytes.Buffer
	written, err := io.CopyN(&buf, body, limit+1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	data := buf.Bytes()
	if written > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

type capturingReadCloser struct {
	body    io.ReadCloser
	limit   int64
	capture bytes.Buffer
	size    int64
}

func newCapturingReadCloser(body io.ReadCloser, limit int64) *capturingReadCloser {
	if body == nil {
		body = http.NoBody
	}
	if limit < 0 {
		limit = 0
	}
	return &capturingReadCloser{body: body, limit: limit}
}

func (c *capturingReadCloser) Read(p []byte) (int, error) {
	n, err := c.body.Read(p)
	c.size += int64(n)

	remaining := c.limit + 1 - int64(c.capture.Len())
	if n > 0 && remaining > 0 {
		if int64(n) < remaining {
			remaining = int64(n)
		}
		_, _ = c.capture.Write(p[:remaining])
	}
	return n, err
}

func (c *capturingReadCloser) Close() error {
	return c.body.Close()
}

func (c *capturingReadCloser) Captured() ([]byte, bool) {
	captured := c.capture.Bytes()
	if c.size > c.limit {
		return append([]byte(nil), captured[:c.limit]...), true
	}
	return append([]byte(nil), captured...), false
}

func (c *capturingReadCloser) Size() int64 {
	return c.size
}
