package proxy

import (
	"bytes"
	"io"
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
