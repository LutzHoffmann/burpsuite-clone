// Package target derives value-free endpoint metadata from captured exchanges.
package target

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lutzifer/burpsuite-clone/internal/store"
	"golang.org/x/net/idna"
)

const (
	diagnosticBodyTruncated          = "body_truncated"
	diagnosticBodyCompressed         = "body_compressed"
	diagnosticBodyUnsupportedMIME    = "body_unsupported_mime"
	diagnosticBodyBinary             = "body_binary"
	diagnosticFormMalformed          = "form_malformed"
	diagnosticJSONMalformed          = "json_malformed"
	diagnosticJSONDepthExceeded      = "json_depth_exceeded"
	diagnosticJSONFieldLimitExceeded = "json_field_limit_exceeded"
	diagnosticMultipartMalformed     = "multipart_malformed"
	diagnosticMultipartFieldLimit    = "multipart_field_limit_exceeded"
)

// Limits bounds request-body traversal. Non-positive values disable the
// corresponding parser rather than allowing unbounded work.
type Limits struct{ MaxJSONDepth, MaxFields, MaxMultipartFields int }

// Analyze produces a disposable observation without retaining parameter values.
func Analyze(exchange *store.Exchange, limits Limits) (store.TargetObservation, error) {
	if exchange == nil {
		return store.TargetObservation{}, errors.New("invalid endpoint identity")
	}
	key, err := normalizeEndpoint(exchange)
	if err != nil {
		return store.TargetObservation{}, err
	}

	requestMIME, _, _ := parseMediaType(http.Header(exchange.Request.Headers).Get("Content-Type"))
	responseMIME, _, _ := parseMediaType(exchange.MIMEType)
	observation := store.TargetObservation{
		Key:          key,
		ExchangeID:   exchange.ID,
		StartedAt:    exchange.StartedAt,
		Status:       exchange.Status,
		RequestMIME:  requestMIME,
		ResponseMIME: responseMIME,
		Error:        exchange.Error,
	}

	parameters := make([]store.TargetParameter, 0)
	parameters = append(parameters, queryParameters(exchange.Query)...)
	parameters = append(parameters, cookieParameters(http.Header(exchange.Request.Headers))...)

	bodyParameters, diagnostic := bodyParameters(exchange, limits)
	parameters = append(parameters, bodyParameters...)
	slices.SortFunc(parameters, compareParameter)
	parameters = slices.CompactFunc(parameters, func(a, b store.TargetParameter) bool {
		return compareParameter(a, b) == 0
	})
	observation.Parameters = parameters
	observation.ParseDiagnostic = diagnostic
	return observation, nil
}

func normalizeEndpoint(exchange *store.Exchange) (store.TargetEndpointKey, error) {
	scheme := strings.ToLower(strings.TrimSpace(exchange.Scheme))
	if scheme != "http" && scheme != "https" {
		return store.TargetEndpointKey{}, errors.New("invalid endpoint identity")
	}
	host, port, err := splitHostPort(exchange.Host, scheme)
	if err != nil {
		return store.TargetEndpointKey{}, errors.New("invalid endpoint identity")
	}
	path := exchange.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return store.TargetEndpointKey{}, errors.New("invalid endpoint identity")
	}
	method := strings.ToUpper(strings.TrimSpace(exchange.Method))
	if method == "" || strings.ContainsAny(method, " \t\r\n") {
		return store.TargetEndpointKey{}, errors.New("invalid endpoint identity")
	}
	return store.TargetEndpointKey{Scheme: scheme, Host: host, Port: port, Path: path, Method: method}, nil
}

func splitHostPort(value, scheme string) (string, int, error) {
	host, portText, err := net.SplitHostPort(value)
	if err == nil {
		if strings.HasPrefix(value, "[") && net.ParseIP(host) == nil {
			return "", 0, errors.New("invalid host")
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return "", 0, errors.New("invalid port")
		}
		normalizedHost, err := normalizeHost(host)
		if err != nil {
			return "", 0, err
		}
		return normalizedHost, port, nil
	}

	host = value
	if strings.HasPrefix(value, "[") || strings.HasSuffix(value, "]") {
		if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
			return "", 0, errors.New("invalid host")
		}
		host = value[1 : len(value)-1]
		if net.ParseIP(host) == nil {
			return "", 0, errors.New("invalid host")
		}
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", 0, errors.New("invalid host")
	}
	normalizedHost, err := normalizeHost(host)
	if err != nil {
		return "", 0, err
	}
	if scheme == "http" {
		return normalizedHost, 80, nil
	}
	return normalizedHost, 443, nil
}

func normalizeHost(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", errors.New("empty host")
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	host, err := idna.Lookup.ToASCII(value)
	if err != nil || host == "" {
		return "", errors.New("invalid host")
	}
	return strings.ToLower(host), nil
}

func queryParameters(rawQuery string) []store.TargetParameter {
	values, _ := url.ParseQuery(rawQuery)
	return namedParameters("query", values)
}

func cookieParameters(headers http.Header) []store.TargetParameter {
	request := &http.Request{Header: headers}
	cookies := request.Cookies()
	parameters := make([]store.TargetParameter, 0, len(cookies))
	for _, cookie := range cookies {
		parameters = append(parameters, targetParameter("cookie", cookie.Name, "string"))
	}
	return parameters
}

func bodyParameters(exchange *store.Exchange, limits Limits) ([]store.TargetParameter, string) {
	headers := http.Header(exchange.Request.Headers)
	if exchange.RequestTruncated {
		return nil, diagnosticBodyTruncated
	}
	if isCompressed(headers) {
		return nil, diagnosticBodyCompressed
	}
	if len(exchange.Request.Body) == 0 {
		return nil, ""
	}
	mediaType, params, err := parseMediaType(headers.Get("Content-Type"))
	if err != nil || !supportedRequestMIME(mediaType) {
		return nil, diagnosticBodyUnsupportedMIME
	}
	if !utf8.Valid(exchange.Request.Body) {
		return nil, diagnosticBodyBinary
	}

	switch mediaType {
	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(exchange.Request.Body))
		if err != nil {
			return nil, diagnosticFormMalformed
		}
		if countValues(values) > limits.MaxFields {
			return nil, diagnosticJSONFieldLimitExceeded
		}
		return namedParameters("form", values), ""
	case "application/json":
		return jsonParameters(exchange.Request.Body, limits)
	case "multipart/form-data":
		return multipartParameters(exchange.Request.Body, params["boundary"], limits.MaxMultipartFields)
	default:
		return nil, diagnosticBodyUnsupportedMIME
	}
}

func parseMediaType(value string) (string, map[string]string, error) {
	if value == "" {
		return "", nil, errors.New("missing media type")
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil {
		return "", nil, err
	}
	return strings.ToLower(mediaType), params, nil
}

func supportedRequestMIME(mediaType string) bool {
	return mediaType == "application/json" || mediaType == "application/x-www-form-urlencoded" || mediaType == "multipart/form-data"
}

func isCompressed(headers http.Header) bool {
	for _, value := range headers.Values("Content-Encoding") {
		for _, encoding := range strings.Split(value, ",") {
			if encoding = strings.TrimSpace(encoding); encoding != "" && !strings.EqualFold(encoding, "identity") {
				return true
			}
		}
	}
	return false
}

func namedParameters(location string, values url.Values) []store.TargetParameter {
	parameters := make([]store.TargetParameter, 0, len(values))
	for name := range values {
		parameters = append(parameters, targetParameter(location, name, "string"))
	}
	return parameters
}

func countValues(values url.Values) int {
	count := 0
	for _, entries := range values {
		count += len(entries)
	}
	return count
}

func jsonParameters(body []byte, limits Limits) ([]store.TargetParameter, string) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, diagnosticJSONMalformed
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, diagnosticJSONMalformed
	}

	type frame struct {
		value any
		path  string
		depth int
	}
	stack := []frame{{value: root, depth: 0}}
	parameters := make([]store.TargetParameter, 0)
	fields := 0
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch value := current.value.(type) {
		case map[string]any:
			if current.depth >= limits.MaxJSONDepth {
				return nil, diagnosticJSONDepthExceeded
			}
			if len(value) == 0 && current.path != "" {
				parameters = append(parameters, targetParameter("json", current.path, "object"))
				continue
			}
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for i := len(keys) - 1; i >= 0; i-- {
				fields++
				if fields > limits.MaxFields {
					return nil, diagnosticJSONFieldLimitExceeded
				}
				key := keys[i]
				stack = append(stack, frame{value: value[key], path: joinJSONPath(current.path, key), depth: current.depth + 1})
			}
		case []any:
			if current.depth >= limits.MaxJSONDepth {
				return nil, diagnosticJSONDepthExceeded
			}
			if len(value) == 0 && current.path != "" {
				parameters = append(parameters, targetParameter("json", current.path, "array"))
				continue
			}
			for i := len(value) - 1; i >= 0; i-- {
				fields++
				if fields > limits.MaxFields {
					return nil, diagnosticJSONFieldLimitExceeded
				}
				stack = append(stack, frame{value: value[i], path: arrayJSONPath(current.path), depth: current.depth + 1})
			}
		default:
			if current.path != "" {
				parameters = append(parameters, targetParameter("json", current.path, jsonValueType(value)))
			}
		}
	}
	return parameters, ""
}

func multipartParameters(body []byte, boundary string, maxFields int) ([]store.TargetParameter, string) {
	if boundary == "" || maxFields <= 0 {
		return nil, diagnosticMultipartFieldLimit
	}
	if !hasMultipartClosingBoundary(body, boundary) {
		return nil, diagnosticMultipartMalformed
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parameters := make([]store.TargetParameter, 0)
	fields := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return parameters, ""
		}
		if err != nil {
			return nil, diagnosticMultipartMalformed
		}
		fields++
		if fields > maxFields {
			_ = part.Close()
			return nil, diagnosticMultipartFieldLimit
		}
		name, isFile, err := multipartPartName(part)
		if err != nil {
			_ = part.Close()
			return nil, diagnosticMultipartMalformed
		}
		if !isFile {
			parameters = append(parameters, targetParameter("multipart", name, "string"))
		}
		_ = part.Close()
	}
}

func multipartPartName(part *multipart.Part) (string, bool, error) {
	disposition, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil || !strings.EqualFold(disposition, "form-data") {
		return "", false, errors.New("invalid multipart disposition")
	}
	name, ok := params["name"]
	if !ok || name == "" {
		return "", false, errors.New("missing multipart field name")
	}
	_, isFile := params["filename"]
	return name, isFile, nil
}

func hasMultipartClosingBoundary(body []byte, boundary string) bool {
	trimmed := bytes.TrimRight(body, "\r\n")
	marker := []byte("--" + boundary + "--")
	if !bytes.HasSuffix(trimmed, marker) {
		return false
	}
	markerStart := len(trimmed) - len(marker)
	return markerStart == 0 || trimmed[markerStart-1] == '\n'
}

func joinJSONPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func arrayJSONPath(prefix string) string {
	return prefix + "[]"
}

func jsonValueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}

func targetParameter(location, name, valueType string) store.TargetParameter {
	return store.TargetParameter{Location: location, Name: name, ValueType: valueType}
}

func compareParameter(a, b store.TargetParameter) int {
	if result := strings.Compare(a.Location, b.Location); result != 0 {
		return result
	}
	if result := strings.Compare(a.Name, b.Name); result != 0 {
		return result
	}
	return strings.Compare(a.ValueType, b.ValueType)
}
