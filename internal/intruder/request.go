package intruder

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
)

func BuildRequest(template Template, positions []Position, combination Combination) (repeater.SendRequest, error) {
	base, err := validateDestination(template)
	if err != nil {
		return repeater.SendRequest{}, err
	}
	if len(template.Raw) > MaxTemplateBytes {
		return repeater.SendRequest{}, invalid("template.raw", "too_large")
	}

	byID := make(map[string]Selection, len(combination.Selections))
	for _, selected := range combination.Selections {
		if _, duplicate := byID[selected.PositionID]; duplicate {
			return repeater.SendRequest{}, invalid("combination.selections", "duplicate")
		}
		byID[selected.PositionID] = selected
	}
	type replacement struct {
		position Position
		payload  []byte
	}
	var replacements []replacement
	for _, position := range positions {
		selected, ok := byID[position.ID]
		if !ok {
			continue
		}
		if position.Start < 0 || position.End <= position.Start || position.End > len(template.Raw) {
			return repeater.SendRequest{}, invalid("positions", "range")
		}
		replacements = append(replacements, replacement{position: position, payload: selected.Payload})
		delete(byID, position.ID)
	}
	if len(byID) != 0 {
		return repeater.SendRequest{}, invalid("combination.selections", "unknown_position")
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].position.Start > replacements[j].position.Start })
	raw := append([]byte(nil), template.Raw...)
	headerEnd := bytes.Index(raw, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		return repeater.SendRequest{}, invalid("template.raw", "parse")
	}
	for _, item := range replacements {
		if item.position.Start < headerEnd && bytes.ContainsAny(item.payload, "\r\n") {
			return repeater.SendRequest{}, invalid("template.raw", "parse")
		}
		raw = append(raw[:item.position.Start], append(append([]byte(nil), item.payload...), raw[item.position.End:]...)...)
	}
	if len(raw) > MaxTemplateBytes {
		return repeater.SendRequest{}, invalid("template.raw", "too_large")
	}
	raw, err = normalizeFraming(raw)
	if err != nil {
		return repeater.SendRequest{}, err
	}

	request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return repeater.SendRequest{}, invalid("template.raw", "parse")
	}
	defer request.Body.Close()
	if request.Method != template.Method {
		return repeater.SendRequest{}, invalid("template.method", "method_changed")
	}
	if request.RequestURI == "" || !strings.HasPrefix(request.RequestURI, "/") || request.URL.IsAbs() {
		return repeater.SendRequest{}, invalid("template.raw", "request_target")
	}
	if !sameAuthority(request.Host, base.Host) {
		return repeater.SendRequest{}, invalid("template.raw", "origin_changed")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, MaxTemplateBytes+1))
	if err != nil || len(body) > MaxTemplateBytes {
		return repeater.SendRequest{}, invalid("template.raw", "too_large")
	}

	headers := request.Header.Clone()
	removeHopByHop(headers)
	headers.Set("Content-Length", strconv.Itoa(len(body)))
	return repeater.SendRequest{
		Method:  request.Method,
		URL:     base.Scheme + "://" + base.Host + request.URL.RequestURI(),
		Headers: headers,
		Body:    body,
	}, nil
}

func validateDestination(template Template) (*url.URL, error) {
	u, err := url.Parse(template.URL)
	if err != nil || u.Host == "" {
		return nil, invalid("template.url", "parse")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, invalid("template.url", "scheme")
	}
	if u.User != nil {
		return nil, invalid("template.url", "userinfo")
	}
	if u.Fragment != "" {
		return nil, invalid("template.url", "fragment")
	}
	return u, nil
}

func normalizeFraming(raw []byte) ([]byte, error) {
	separator := bytes.Index(raw, []byte("\r\n\r\n"))
	if separator < 0 {
		return nil, invalid("template.raw", "parse")
	}
	head := strings.Split(string(raw[:separator]), "\r\n")
	if len(head) < 2 {
		return nil, invalid("template.raw", "parse")
	}
	hasLength, hasTransfer := false, false
	filtered := []string{head[0]}
	for _, line := range head[1:] {
		colon := strings.IndexByte(line, ':')
		if colon < 1 {
			return nil, invalid("template.raw", "parse")
		}
		name := strings.TrimSpace(line[:colon])
		switch strings.ToLower(name) {
		case "content-length":
			hasLength = true
			continue
		case "transfer-encoding":
			hasTransfer = true
			continue
		}
		filtered = append(filtered, line)
	}
	if hasLength && hasTransfer {
		return nil, invalid("template.raw", "framing")
	}
	body := raw[separator+4:]
	filtered = append(filtered, fmt.Sprintf("Content-Length: %d", len(body)))
	return append([]byte(strings.Join(filtered, "\r\n")+"\r\n\r\n"), body...), nil
}

func sameAuthority(left, right string) bool {
	l, errLeft := url.Parse("http://" + left)
	r, errRight := url.Parse("http://" + right)
	return errLeft == nil && errRight == nil && strings.EqualFold(l.Hostname(), r.Hostname()) && effectivePort(l) == effectivePort(r)
}

func effectivePort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	return "80"
}

func removeHopByHop(headers http.Header) {
	for _, token := range strings.Split(headers.Get("Connection"), ",") {
		headers.Del(strings.TrimSpace(token))
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}
