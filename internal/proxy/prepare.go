package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
)

type editableBody struct {
	data                []byte
	editable, truncated bool
}

func bodyCandidate(h http.Header) bool {
	encoding := strings.TrimSpace(h.Get("Content-Encoding"))
	mime := strings.ToLower(h.Get("Content-Type"))
	return (encoding == "" || strings.EqualFold(encoding, "identity")) && !strings.Contains(mime, "text/event-stream") && !strings.Contains(mime, "multipart/") && (mime == "" || isTextSafe(mime, []byte("x")))
}

// A prefix is always replayed, including when the size limit or text check fails.
func inspectBody(body *io.ReadCloser, h http.Header, length, limit int64, needed bool) (editableBody, error) {
	if *body == nil {
		*body = http.NoBody
	}
	if !needed || !bodyCandidate(h) || length < 0 {
		return editableBody{}, nil
	}
	if length > limit {
		return editableBody{truncated: true}, nil
	}
	data, large, err := readInterceptBody(*body, limit)
	if err != nil {
		_ = (*body).Close()
		return editableBody{}, err
	}
	if large {
		*body = &prefixReadCloser{Reader: io.MultiReader(bytes.NewReader(data), *body), closer: *body}
		return editableBody{truncated: true}, nil
	}
	_ = (*body).Close()
	*body = io.NopCloser(bytes.NewReader(data))
	return editableBody{data: data, editable: isTextSafe(h.Get("Content-Type"), data)}, nil
}

func matchRequest(r *http.Request, h http.Header, status int) intercept.MatchRequest {
	return intercept.MatchRequest{Method: r.Method, Host: r.URL.Host, Path: r.URL.Path, MIME: h.Get("Content-Type"), StatusCode: status}
}

func matchesRules(enabled bool, rules []intercept.Rule, m intercept.MatchRequest) bool {
	if !enabled {
		return false
	}
	for _, rule := range rules {
		if intercept.Matches(rule, m) {
			return true
		}
	}
	return false
}

func needsBody(state intercept.ControllerState, direction string, m intercept.MatchRequest, manual bool) bool {
	if manual {
		return true
	}
	for _, r := range state.ReplacementRules {
		if r.Target == "body" && r.Enabled && r.Direction == direction {
			return true
		}
	}
	return false
}

func applyReplacements(state intercept.ControllerState, direction string, request *http.Request, h http.Header, body *editableBody, limit int64, status int) ([]string, bool, error) {
	var ids []string
	bodyChanged := false
	for _, rule := range state.ReplacementRules {
		if !rule.Matches(direction, matchRequest(request, h, status)) {
			continue
		}
		changed := false
		switch rule.Target {
		case "url":
			value, c, err := rule.Replace(request.URL.String(), 64*1024)
			if err != nil {
				return ids, bodyChanged, err
			}
			if c {
				if err := intercept.ValidateURL(value); err != nil {
					return ids, bodyChanged, err
				}
				request.URL, _ = url.Parse(value)
				request.Host = request.URL.Host
				changed = true
			}
		case "header":
			key := http.CanonicalHeaderKey(rule.Header)
			values := append([]string(nil), h.Values(key)...)
			for i, value := range values {
				v, c, err := rule.Replace(value, 64*1024)
				if err != nil {
					return ids, bodyChanged, err
				}
				values[i] = v
				changed = changed || c
			}
			if changed {
				h[key] = values
				if err := intercept.ValidateHeaders(h); err != nil {
					return ids, bodyChanged, err
				}
			}
		case "body":
			if !body.editable {
				continue
			}
			value, c, err := rule.Replace(string(body.data), limit)
			if err != nil {
				return ids, bodyChanged, err
			}
			if c {
				body.data = []byte(value)
				changed = true
				bodyChanged = true
			}
		}
		if changed {
			ids = append(ids, rule.ID)
		}
	}
	return ids, bodyChanged, nil
}

// Metadata describing untouched wire bytes may not be changed by an operator.
func editedHeaders(original http.Header, edit map[string][]string) (http.Header, error) {
	if edit == nil {
		return original.Clone(), nil
	}
	if err := intercept.ValidateHeaders(edit); err != nil {
		return nil, err
	}
	result := intercept.CanonicalHeaders(edit)
	for key, values := range original {
		if intercept.ProtectedHeader(key) {
			if v, ok := result[key]; ok && !reflect.DeepEqual(v, values) {
				return nil, fmt.Errorf("cannot edit protected header %s", key)
			}
			result[key] = append([]string(nil), values...)
		}
	}
	for key := range result {
		if intercept.ProtectedHeader(key) && original.Values(key) == nil {
			return nil, fmt.Errorf("cannot add protected header %s", key)
		}
	}
	return result, nil
}

func setRequestBody(r *http.Request, data []byte) {
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.TransferEncoding = nil
	r.Trailer = nil
	r.GetBody = nil
	r.Header.Del("Content-Length")
	r.Header.Del("Transfer-Encoding")
	r.Header.Del("Trailer")
	if len(data) > 0 {
		r.Header.Set("Content-Length", strconv.Itoa(len(data)))
	}
}

func (s *Server) prepareRequest(request *http.Request, decision scope.Decision, rules *scope.RuleSet) (*preparedRequest, error) {
	if !decision.InScope || s.cfg.Intercept == nil {
		return prepareStreamingRequest(request, s.cfg.BodyLimitBytes, decision), nil
	}
	state := s.cfg.Intercept.State()
	forward := request.Clone(request.Context())
	forward.Header = request.Header.Clone()
	if forward.Body == nil {
		forward.Body = http.NoBody
	}
	m := matchRequest(forward, forward.Header, 0)
	manual := matchesRules(state.Enabled, state.Rules, m)
	// Header/URL replacements can enable a manual rule; buffer only when that is possible.
	need := needsBody(state, "request", m, manual || state.Enabled && len(state.ReplacementRules) > 0)
	body, err := inspectBody(&forward.Body, forward.Header, forward.ContentLength, s.cfg.BodyLimitBytes, need)
	if err != nil {
		return nil, err
	}
	ids, changed, err := applyReplacements(state, "request", forward, forward.Header, &body, s.cfg.BodyLimitBytes, 0)
	if err != nil {
		_ = forward.Body.Close()
		return nil, err
	}
	if changed {
		setRequestBody(forward, body.data)
	}
	result := &preparedRequest{decision: decision, appliedRuleIDs: ids}
	editedDecision := classifyWithRules(rules, forward)
	if !editedDecision.InScope {
		result.scopeRejected = true
		result.decision = editedDecision
	} else if matchesRules(state.Enabled, state.Rules, matchRequest(forward, forward.Header, 0)) {
		result.intercepted = true
		item := intercept.Item{ID: strconv.FormatUint(s.nextID.Add(1), 10), Method: forward.Method, URL: forward.URL.String(), Headers: forward.Header.Clone(), Body: body.data, BodyEditable: body.editable, BodyTruncated: body.truncated}
		d, err := s.cfg.Intercept.Queue().Enqueue(request.Context(), item)
		if err != nil {
			_ = forward.Body.Close()
			return nil, err
		}
		if d.Action == intercept.ActionDrop {
			result.dropped = true
		} else {
			if d.Edit.Method != "" {
				if !intercept.ValidToken(d.Edit.Method) {
					_ = forward.Body.Close()
					return nil, fmt.Errorf("invalid request method")
				}
				forward.Method = d.Edit.Method
			}
			if d.Edit.URL != "" {
				if err := intercept.ValidateURL(d.Edit.URL); err != nil {
					_ = forward.Body.Close()
					return nil, err
				}
				forward.URL, _ = url.Parse(d.Edit.URL)
				forward.Host = forward.URL.Host
			}
			forward.Header, err = editedHeaders(forward.Header, d.Edit.Headers)
			if err != nil {
				_ = forward.Body.Close()
				return nil, err
			}
			if !body.editable && (d.Edit.BodySet || len(d.Edit.Body) > 0 && !bytes.Equal(d.Edit.Body, body.data)) {
				_ = forward.Body.Close()
				return nil, fmt.Errorf("body is not editable")
			}
			if body.editable && (d.Edit.BodySet || d.Edit.Body != nil) {
				if !body.editable || int64(len(d.Edit.Body)) > s.cfg.BodyLimitBytes {
					_ = forward.Body.Close()
					return nil, fmt.Errorf("body is not editable or exceeds limit")
				}
				body.data = d.Edit.Body
				setRequestBody(forward, body.data)
			}
			editedDecision = classifyWithRules(rules, forward)
			result.decision = editedDecision
			result.scopeRejected = !editedDecision.InScope
		}
	}
	prepared := prepareStreamingRequest(forward, s.cfg.BodyLimitBytes, result.decision)
	result.forward = prepared.forward
	result.capture = prepared.capture
	result.headers = prepared.forward.Header.Clone()
	result.droppedBody = body.data
	if result.dropped || result.scopeRejected {
		_ = result.capture.Close()
	}
	return result, nil
}

func responseHasBody(method string, status int) bool {
	return method != http.MethodHead && status >= 200 && status != 204 && status != 205 && status != 304
}

func (s *Server) prepareResponse(prepared *preparedRequest, response *http.Response) *http.Response {
	request := prepared.forward
	response.Request = request
	if response.Body == nil {
		response.Body = http.NoBody
	}
	if !prepared.decision.InScope || s.cfg.Intercept == nil {
		return response
	}
	state := s.cfg.Intercept.State()
	fail := func(err error) *http.Response {
		_ = response.Body.Close()
		prepared.responseError = err
		data := []byte("response intercepted: " + err.Error() + "\n")
		if request.Method == http.MethodHead {
			data = nil
		}
		return &http.Response{StatusCode: 502, Status: "502 Bad Gateway", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Length": {strconv.Itoa(len(data))}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: request}
	}
	m := matchRequest(request, response.Header, response.StatusCode)
	manual := matchesRules(state.ResponseEnabled, state.ResponseRules, m)
	hasBody := responseHasBody(request.Method, response.StatusCode)
	body, err := inspectBody(&response.Body, response.Header, response.ContentLength, s.cfg.BodyLimitBytes, hasBody && needsBody(state, "response", m, manual || state.ResponseEnabled && len(state.ReplacementRules) > 0))
	if err != nil {
		return fail(err)
	}
	ids, changed, err := applyReplacements(state, "response", request, response.Header, &body, s.cfg.BodyLimitBytes, response.StatusCode)
	prepared.appliedRuleIDs = append(prepared.appliedRuleIDs, ids...)
	if err != nil {
		return fail(err)
	}
	if matchesRules(state.ResponseEnabled, state.ResponseRules, matchRequest(request, response.Header, response.StatusCode)) {
		prepared.responseIntercepted = true
		item := intercept.Item{ID: strconv.FormatUint(s.nextID.Add(1), 10), Phase: "response", StatusCode: response.StatusCode, Method: request.Method, URL: request.URL.String(), Headers: response.Header.Clone(), Body: body.data, BodyEditable: body.editable, BodyTruncated: body.truncated}
		d, err := s.cfg.Intercept.ResponseQueue().Enqueue(request.Context(), item)
		if err != nil {
			return fail(err)
		}
		if d.Action == intercept.ActionDrop {
			return fail(fmt.Errorf("response dropped by operator"))
		}
		if d.Edit.StatusCode != 0 {
			if d.Edit.StatusCode < 200 || d.Edit.StatusCode > 599 {
				return fail(fmt.Errorf("invalid response status"))
			}
			response.StatusCode = d.Edit.StatusCode
			response.Status = fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode))
		}
		response.Header, err = editedHeaders(response.Header, d.Edit.Headers)
		if err != nil {
			return fail(err)
		}
		if !body.editable && (d.Edit.BodySet || len(d.Edit.Body) > 0 && !bytes.Equal(d.Edit.Body, body.data)) {
			return fail(fmt.Errorf("body is not editable"))
		}
		if body.editable && (d.Edit.BodySet || d.Edit.Body != nil) {
			if !body.editable || int64(len(d.Edit.Body)) > s.cfg.BodyLimitBytes {
				return fail(fmt.Errorf("body is not editable or exceeds limit"))
			}
			body.data = d.Edit.Body
			changed = true
		}
	}
	if !responseHasBody(request.Method, response.StatusCode) {
		_ = response.Body.Close()
		response.Body = http.NoBody
		response.TransferEncoding = nil
		response.Trailer = nil
		response.Header.Del("Transfer-Encoding")
		response.Header.Del("Trailer")
		if request.Method != http.MethodHead && response.StatusCode != 304 {
			response.ContentLength = 0
			response.Header.Del("Content-Length")
		}
	} else if changed {
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body.data))
		response.ContentLength = int64(len(body.data))
		response.TransferEncoding = nil
		response.Trailer = nil
		response.Header.Del("Transfer-Encoding")
		response.Header.Del("Trailer")
		response.Header.Set("Content-Length", strconv.Itoa(len(body.data)))
	} else if !hasBody {
		response.ContentLength = 0
		response.Header.Set("Content-Length", "0")
	}
	return response
}
