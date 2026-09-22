package intruder

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
)

func TestBuildRequestAppliesBytePositionsAndRebuildsFraming(t *testing.T) {
	raw := []byte("POST /caf%C3%A9?q=old HTTP/1.1\r\nHost: example.test\r\nX-Value: first\r\nX-Value: second\r\nConnection: keep-alive\r\nContent-Length: 3\r\n\r\na\x00b")
	query := bytes.Index(raw, []byte("old"))
	body := bytes.LastIndex(raw, []byte{'a', 0, 'b'})
	cfg := Template{Method: "POST", URL: "https://example.test/caf%C3%A9?q=old", Raw: raw}
	positions := []Position{{ID: "query", Start: query, End: query + 3}, {ID: "body", Start: body, End: body + 3}}
	combination := Combination{Selections: []Selection{
		{PositionID: "query", Payload: []byte("new")},
		{PositionID: "body", Payload: []byte{'x', 0, 'y', 'z'}},
	}}

	got, err := BuildRequest(cfg, positions, combination)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.URL != "https://example.test/caf%C3%A9?q=new" {
		t.Fatalf("request line = %s %s", got.Method, got.URL)
	}
	if !bytes.Equal(got.Body, []byte{'x', 0, 'y', 'z'}) {
		t.Fatalf("body = %v", got.Body)
	}
	if values := got.Headers["X-Value"]; fmt.Sprint(values) != "[first second]" {
		t.Fatalf("ordered repeated values = %v", values)
	}
	if len(got.Headers["Connection"]) != 0 {
		t.Fatalf("hop-by-hop header retained: %#v", got.Headers)
	}
	if values := got.Headers["Content-Length"]; len(values) != 1 || values[0] != "4" {
		t.Fatalf("content length = %v", values)
	}
}

func TestBuildRequestSupportsAdjacentAndEmptyPayloads(t *testing.T) {
	raw := []byte("POST / HTTP/1.1\r\nHost: example.test\r\nContent-Length: 2\r\n\r\nab")
	start := bytes.LastIndex(raw, []byte("ab"))
	got, err := BuildRequest(
		Template{Method: "POST", URL: "http://example.test/", Raw: raw},
		[]Position{{ID: "a", Start: start, End: start + 1}, {ID: "b", Start: start + 1, End: start + 2}},
		Combination{Selections: []Selection{{PositionID: "a", Payload: nil}, {PositionID: "b", Payload: []byte{0xff}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if values := got.Headers["Content-Length"]; !bytes.Equal(got.Body, []byte{0xff}) || len(values) != 1 || values[0] != "1" {
		t.Fatalf("body/framing = %v / %v", got.Body, values)
	}
}

func TestBuildRequestRejectsUnsafeOrChangedRequests(t *testing.T) {
	base := func(raw string) Template {
		return Template{Method: "POST", URL: "https://example.test/base", Raw: []byte(raw)}
	}
	tests := []struct {
		name     string
		template Template
		position Position
		payload  []byte
		code     string
	}{
		{"changed method", base("POST /base HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\n\r\n"), Position{ID: "p", Start: 0, End: 4}, []byte("GET"), "method_changed"},
		{"changed host", base("POST /base HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\n\r\n"), rangeOf("POST /base HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\n\r\n", "example.test"), []byte("evil.test"), "origin_changed"},
		{"absolute target", base("POST /base HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\n\r\n"), rangeOf("POST /base HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\n\r\n", "/base"), []byte("https://evil.test/x"), "request_target"},
		{"header newline", base("POST /base HTTP/1.1\r\nHost: example.test\r\nX-Test: safe\r\nContent-Length: 0\r\n\r\n"), rangeOf("POST /base HTTP/1.1\r\nHost: example.test\r\nX-Test: safe\r\nContent-Length: 0\r\n\r\n", "safe"), []byte("ok\r\nInjected: yes"), "parse"},
		{"conflicting framing", base("POST /base HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\nTransfer-Encoding: chunked\r\n\r\n"), rangeOf("POST /base HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\nTransfer-Encoding: chunked\r\n\r\n", "/base"), []byte("/base"), "framing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.position.ID = "p"
			_, err := BuildRequest(tc.template, []Position{tc.position}, Combination{Selections: []Selection{{PositionID: "p", Payload: tc.payload}}})
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Code != tc.code {
				t.Fatalf("error = %v, want code %q", err, tc.code)
			}
		})
	}
}

func TestBuildRequestRejectsInvalidTemplateDestinationAndSize(t *testing.T) {
	raw := []byte("GET / HTTP/1.1\r\nHost: example.test\r\n\r\n")
	position := Position{ID: "p", Start: 4, End: 5}
	tests := []struct {
		name string
		url  string
		raw  []byte
		code string
	}{
		{"scheme", "ftp://example.test/", raw, "scheme"},
		{"userinfo", "https://user@example.test/", raw, "userinfo"},
		{"fragment", "https://example.test/#part", raw, "fragment"},
		{"too large", "https://example.test/", make([]byte, MaxTemplateBytes+1), "too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := position
			if len(tc.raw) > len(raw) {
				p = Position{ID: "p", Start: 0, End: 1}
			}
			_, err := BuildRequest(Template{Method: "GET", URL: tc.url, Raw: tc.raw}, []Position{p}, Combination{Selections: []Selection{{PositionID: "p", Payload: []byte("/")}}})
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Code != tc.code {
				t.Fatalf("error = %v, want %q", err, tc.code)
			}
		})
	}
}

func rangeOf(raw, part string) Position {
	start := strings.Index(raw, part)
	return Position{Start: start, End: start + len(part)}
}

var _ repeater.SendRequest
