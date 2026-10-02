package scope

import (
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

type Action string

const (
	ActionInclude Action = "include"
	ActionExclude Action = "exclude"
)

type Rule struct {
	ID          int64  `json:"id"`
	Enabled     bool   `json:"enabled"`
	Action      Action `json:"action"`
	Scheme      string `json:"scheme"`
	HostPattern string `json:"hostPattern"`
	Port        int    `json:"port"`
	PathPrefix  string `json:"pathPrefix"`
}

type State struct {
	Version int64  `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Target struct{ Scheme, Host, Path string }

type Decision struct {
	InScope bool
	RuleID  *int64
	Version int64
	Reason  string
}

type RuleSet struct {
	version int64
	rules   []compiledRule
}

type compiledRule struct {
	rule     Rule
	wildcard bool
}

// Compile validates and copies rules into an immutable matcher.
func Compile(version int64, rules []Rule) (*RuleSet, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for i, input := range rules {
		rule, wildcard, err := normalizeRule(input)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		compiled = append(compiled, compiledRule{rule: rule, wildcard: wildcard})
	}
	return &RuleSet{version: version, rules: compiled}, nil
}

func (s *RuleSet) Classify(target Target) Decision {
	decision := Decision{Version: s.version, Reason: "no_include"}
	normalized, ok := normalizeTarget(target)
	if !ok {
		return decision
	}

	for _, candidate := range s.rules {
		if candidate.rule.Action == ActionExclude && candidate.matches(normalized) {
			return candidate.decision(s.version, false, "excluded")
		}
	}
	for _, candidate := range s.rules {
		if candidate.rule.Action == ActionInclude && candidate.matches(normalized) {
			return candidate.decision(s.version, true, "included")
		}
	}
	return decision
}

func (r compiledRule) decision(version int64, inScope bool, reason string) Decision {
	id := r.rule.ID
	return Decision{InScope: inScope, RuleID: &id, Version: version, Reason: reason}
}

func (r compiledRule) matches(target normalizedTarget) bool {
	if !r.rule.Enabled {
		return false
	}
	if r.rule.Scheme != "" && r.rule.Scheme != target.scheme {
		return false
	}
	if r.rule.Port != 0 && r.rule.Port != target.port {
		return false
	}
	if r.wildcard {
		if !strings.HasSuffix(target.host, "."+r.rule.HostPattern) {
			return false
		}
	} else if r.rule.HostPattern != target.host {
		return false
	}
	return pathMatches(r.rule.PathPrefix, target.path)
}

func normalizeRule(input Rule) (Rule, bool, error) {
	rule := input
	rule.Scheme = strings.ToLower(rule.Scheme)
	if rule.Scheme != "" && rule.Scheme != "http" && rule.Scheme != "https" {
		return Rule{}, false, fmt.Errorf("unsupported scheme %q", input.Scheme)
	}
	if rule.Action != ActionInclude && rule.Action != ActionExclude {
		return Rule{}, false, fmt.Errorf("unsupported action %q", input.Action)
	}
	if rule.Port < 0 || rule.Port > 65535 {
		return Rule{}, false, fmt.Errorf("port %d is outside 1..65535", rule.Port)
	}
	if rule.PathPrefix != "" && !strings.HasPrefix(rule.PathPrefix, "/") {
		return Rule{}, false, fmt.Errorf("path prefix %q must start with /", input.PathPrefix)
	}
	if rule.PathPrefix == "" {
		rule.PathPrefix = "/"
	}

	wildcard := strings.HasPrefix(rule.HostPattern, "*.")
	hostPattern := rule.HostPattern
	if wildcard {
		hostPattern = strings.TrimPrefix(hostPattern, "*.")
	}
	if hostPattern == "" || strings.Contains(hostPattern, "*") {
		return Rule{}, false, fmt.Errorf("invalid host pattern %q", input.HostPattern)
	}
	host, err := normalizeHost(hostPattern)
	if err != nil {
		return Rule{}, false, fmt.Errorf("invalid host pattern %q: %w", input.HostPattern, err)
	}
	rule.HostPattern = host
	return rule, wildcard, nil
}

type normalizedTarget struct {
	scheme string
	host   string
	port   int
	path   string
}

func normalizeTarget(target Target) (normalizedTarget, bool) {
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return normalizedTarget{}, false
	}
	host, port, err := splitHostPort(target.Host, scheme)
	if err != nil {
		return normalizedTarget{}, false
	}
	targetPath := target.Path
	if targetPath == "" {
		targetPath = "/"
	}
	if !strings.HasPrefix(targetPath, "/") {
		return normalizedTarget{}, false
	}
	targetPath = cleanTargetPath(targetPath)
	return normalizedTarget{scheme: scheme, host: host, port: port, path: targetPath}, true
}

func cleanTargetPath(value string) string {
	cleaned := path.Clean(value)
	if strings.HasSuffix(value, "/") && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

func splitHostPort(value, scheme string) (string, int, error) {
	host, portText, err := net.SplitHostPort(value)
	if err == nil {
		if strings.HasPrefix(value, "[") && net.ParseIP(host) == nil {
			return "", 0, fmt.Errorf("invalid bracketed host %q", value)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("invalid port %q", portText)
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
			return "", 0, fmt.Errorf("invalid host %q", value)
		}
		host = value[1 : len(value)-1]
		if net.ParseIP(host) == nil {
			return "", 0, fmt.Errorf("invalid bracketed host %q", value)
		}
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", 0, fmt.Errorf("invalid host %q", value)
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
		return "", fmt.Errorf("host is empty")
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	host, err := idna.Lookup.ToASCII(value)
	if err != nil || host == "" {
		return "", fmt.Errorf("invalid DNS host")
	}
	return strings.ToLower(host), nil
}

func pathMatches(prefix, candidate string) bool {
	if prefix == "/" {
		return true
	}
	if !strings.HasPrefix(candidate, prefix) {
		return false
	}
	return len(candidate) == len(prefix) || candidate[len(prefix)] == '/'
}
