package intercept

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxReplacementMatches = 10000
const maxReplacementCaptures = 64

type ReplacementRule struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	Direction    string `json:"direction"`
	Target       string `json:"target"`
	Header       string `json:"header"`
	Pattern      string `json:"pattern"`
	Replacement  string `json:"replacement"`
	Regex        bool   `json:"regex"`
	HostContains string `json:"hostContains"`
	PathContains string `json:"pathContains"`
	MIMEContains string `json:"mimeContains"`
}

func ProtectedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "content-encoding", "transfer-encoding", "connection", "trailer", "te", "upgrade", "keep-alive", "proxy-connection", "proxy-authenticate", "proxy-authorization":
		return true
	}
	return false
}

func ValidToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

func ValidateHeaders(h map[string][]string) error {
	seen := map[string]bool{}
	for k, vv := range h {
		key := strings.ToLower(k)
		if !ValidToken(k) || seen[key] {
			return fmt.Errorf("invalid or duplicate header name")
		}
		seen[key] = true
		for _, v := range vv {
			for _, c := range v {
				if c == 127 || c < 32 && c != '\t' {
					return fmt.Errorf("invalid header value")
				}
			}
		}
	}
	return nil
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\x00") {
		return fmt.Errorf("invalid intercepted request URL")
	}
	return nil
}

func ValidateState(s ControllerState) error {
	if len(s.Rules) > 100 || len(s.ResponseRules) > 100 || len(s.ReplacementRules) > 100 {
		return fmt.Errorf("maximum 100 rules per list")
	}
	for _, rules := range [][]Rule{s.Rules, s.ResponseRules} {
		for _, r := range rules {
			if r.StatusCode != 0 && (r.StatusCode < 200 || r.StatusCode > 599) {
				return fmt.Errorf("invalid status code")
			}
			if r.Method != "" && !ValidToken(r.Method) {
				return fmt.Errorf("invalid method")
			}
			if len(r.HostContains) > 4096 || len(r.PathContains) > 4096 || len(r.MIMEContains) > 4096 {
				return fmt.Errorf("rule filter too long")
			}
		}
	}
	ids := map[string]bool{}
	for _, r := range s.ReplacementRules {
		if r.ID == "" || len(r.ID) > 4096 || ids[r.ID] {
			return fmt.Errorf("invalid or duplicate replacement ID")
		}
		ids[r.ID] = true
		if r.Direction != "request" && r.Direction != "response" {
			return fmt.Errorf("invalid direction")
		}
		if r.Target != "url" && r.Target != "header" && r.Target != "body" || r.Direction == "response" && r.Target == "url" {
			return fmt.Errorf("invalid replacement target")
		}
		if r.Target == "header" && (!ValidToken(r.Header) || ProtectedHeader(r.Header)) {
			return fmt.Errorf("invalid or protected header")
		}
		if r.Pattern == "" || len(r.Pattern) > 4096 || len(r.Replacement) > 4096 || len(r.HostContains) > 4096 || len(r.PathContains) > 4096 || len(r.MIMEContains) > 4096 {
			return fmt.Errorf("replacement pattern or value exceeds limits")
		}
		if r.Regex {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return fmt.Errorf("invalid regex: %w", err)
			}
			if re.NumSubexp() > maxReplacementCaptures {
				return fmt.Errorf("maximum %d capture groups", maxReplacementCaptures)
			}
		}
	}
	return nil
}

func (r ReplacementRule) Matches(direction string, m MatchRequest) bool {
	return r.Enabled && r.Direction == direction && Matches(Rule{Enabled: true, HostContains: r.HostContains, PathContains: r.PathContains, MIMEContains: r.MIMEContains}, m)
}

// Replace bounds both capture expansion and aggregate output before allocation.
func (r ReplacementRule) Replace(input string, limit int64) (string, bool, error) {
	var re *regexp.Regexp
	var err error
	if r.Regex {
		re, err = regexp.Compile(r.Pattern)
	} else {
		re, err = regexp.Compile(regexp.QuoteMeta(r.Pattern))
	}
	if err != nil {
		return "", false, err
	}
	if re.NumSubexp() > maxReplacementCaptures {
		return "", false, fmt.Errorf("maximum %d capture groups", maxReplacementCaptures)
	}
	var out strings.Builder
	appendPart := func(p string) error {
		if int64(out.Len())+int64(len(p)) > limit {
			return fmt.Errorf("replacement output exceeds limit")
		}
		out.WriteString(p)
		return nil
	}
	matches := re.FindAllStringSubmatchIndex(input, maxReplacementMatches+1)
	if len(matches) > maxReplacementMatches {
		return "", false, fmt.Errorf("maximum %d replacements per message field", maxReplacementMatches)
	}
	if len(matches) == 0 {
		return input, false, nil
	}
	pos := 0
	for _, m := range matches {
		if err := appendPart(input[pos:m[0]]); err != nil {
			return "", false, err
		}
		if r.Regex {
			// Expand template tokens separately so a single expansion cannot exceed the bound.
			template := r.Replacement
			for len(template) > 0 {
				i := strings.IndexByte(template, '$')
				if i < 0 {
					if err := appendPart(template); err != nil {
						return "", false, err
					}
					break
				}
				if err := appendPart(template[:i]); err != nil {
					return "", false, err
				}
				template = template[i:]
				end := replacementTokenEnd(template)
				token := string(re.ExpandString(nil, template[:end], input, m))
				if err := appendPart(token); err != nil {
					return "", false, err
				}
				template = template[end:]
			}
		} else if err := appendPart(r.Replacement); err != nil {
			return "", false, err
		}
		pos = m[1]
	}
	if err := appendPart(input[pos:]); err != nil {
		return "", false, err
	}
	result := out.String()
	return result, result != input, nil
}

// Isolate one valid template token so ExpandString cannot allocate multiple
// captured bodies before the output limit is checked.
func replacementTokenEnd(template string) int {
	if len(template) < 2 {
		return 1
	}
	if template[1] == '$' {
		return 2
	}
	start := 1
	braced := template[start] == '{'
	if braced {
		start++
	}
	end := start
	for end < len(template) {
		r, size := utf8.DecodeRuneInString(template[end:])
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		end += size
	}
	if end == start {
		return 1
	}
	if braced {
		if end == len(template) || template[end] != '}' {
			return 1
		}
		end++
	}
	return end
}

func CanonicalHeaders(h map[string][]string) http.Header {
	result := make(http.Header, len(h))
	for k, v := range h {
		result[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
	}
	return result
}
