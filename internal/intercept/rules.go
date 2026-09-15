package intercept

import "strings"

type Rule struct {
	StatusCode   int    `json:"statusCode,omitempty"`
	Enabled      bool   `json:"enabled"`
	Method       string `json:"method"`
	HostContains string `json:"hostContains"`
	PathContains string `json:"pathContains"`
	MIMEContains string `json:"mimeContains"`
}

type MatchRequest struct {
	StatusCode int
	Method     string
	Host       string
	Path       string
	MIME       string
}

func Matches(rule Rule, req MatchRequest) bool {
	if rule.StatusCode != 0 && rule.StatusCode != req.StatusCode {
		return false
	}
	if !rule.Enabled {
		return false
	}
	if rule.Method != "" && !strings.EqualFold(rule.Method, req.Method) {
		return false
	}
	if rule.HostContains != "" && !strings.Contains(strings.ToLower(req.Host), strings.ToLower(rule.HostContains)) {
		return false
	}
	if rule.PathContains != "" && !strings.Contains(strings.ToLower(req.Path), strings.ToLower(rule.PathContains)) {
		return false
	}
	if rule.MIMEContains != "" && !strings.Contains(strings.ToLower(req.MIME), strings.ToLower(rule.MIMEContains)) {
		return false
	}
	return true
}
