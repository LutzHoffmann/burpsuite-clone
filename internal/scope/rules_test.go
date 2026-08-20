package scope

import "testing"

func TestRuleSetClassifyEmptyRulesFailsClosed(t *testing.T) {
	rules, err := Compile(5, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision := rules.Classify(Target{Scheme: "https", Host: "example.test", Path: "/"})
	if decision.InScope || decision.RuleID != nil || decision.Reason != "no_include" || decision.Version != 5 {
		t.Fatalf("empty rules decision = %#v", decision)
	}
}

func TestRuleSetClassifyRejectsMalformedBracketedHost(t *testing.T) {
	rules, err := Compile(5, []Rule{{ID: 1, Enabled: true, Action: ActionInclude, HostPattern: "example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	decision := rules.Classify(Target{Scheme: "https", Host: "[example.test]", Path: "/"})
	if decision.InScope || decision.Reason != "no_include" {
		t.Fatalf("malformed host decision = %#v", decision)
	}
}

func TestRuleSetClassifyHonorsExcludePrecedence(t *testing.T) {
	rules, err := Compile(7, []Rule{
		{ID: 1, Enabled: true, Action: ActionInclude, Scheme: "https", HostPattern: "*.example.test", PathPrefix: "/api"},
		{ID: 2, Enabled: true, Action: ActionExclude, HostPattern: "admin.example.test", PathPrefix: "/api/private"},
	})
	if err != nil {
		t.Fatal(err)
	}

	allowed := rules.Classify(Target{Scheme: "HTTPS", Host: "shop.example.test:443", Path: "/api/orders"})
	if !allowed.InScope || allowed.RuleID == nil || *allowed.RuleID != 1 || allowed.Version != 7 || allowed.Reason != "included" {
		t.Fatalf("allowed decision = %#v", allowed)
	}
	blocked := rules.Classify(Target{Scheme: "https", Host: "admin.example.test", Path: "/api/private/keys"})
	if blocked.InScope || blocked.RuleID == nil || *blocked.RuleID != 2 || blocked.Reason != "excluded" {
		t.Fatalf("blocked decision = %#v", blocked)
	}
}

func TestRuleSetClassifyRequiresIncludeAndPathBoundary(t *testing.T) {
	rules, err := Compile(3, []Rule{{ID: 4, Enabled: true, Action: ActionInclude, HostPattern: "example.test", PathPrefix: "/api"}})
	if err != nil {
		t.Fatal(err)
	}
	if rules.Classify(Target{Scheme: "http", Host: "example.test", Path: "/apiv2"}).InScope {
		t.Fatal("/apiv2 matched /api prefix")
	}
	if !rules.Classify(Target{Scheme: "http", Host: "example.test:80", Path: "/api/v1"}).InScope {
		t.Fatal("default HTTP port or path boundary did not normalize")
	}
	decision := rules.Classify(Target{Scheme: "http", Host: "other.test", Path: "/api"})
	if decision.InScope || decision.RuleID != nil || decision.Reason != "no_include" || decision.Version != 3 {
		t.Fatalf("no include decision = %#v", decision)
	}
}

func TestRuleSetClassifyMatchesSupportedTargets(t *testing.T) {
	tests := []struct {
		name   string
		rule   Rule
		target Target
		match  bool
	}{
		{
			name:   "empty scheme port and path are unrestricted",
			rule:   Rule{ID: 1, Enabled: true, Action: ActionInclude, HostPattern: "example.test"},
			target: Target{Scheme: "https", Host: "example.test:443", Path: "/anywhere"},
			match:  true,
		},
		{
			name:   "disabled rule does not match",
			rule:   Rule{ID: 2, Enabled: false, Action: ActionInclude, HostPattern: "example.test"},
			target: Target{Scheme: "https", Host: "example.test", Path: "/"},
			match:  false,
		},
		{
			name:   "exact host is case insensitive",
			rule:   Rule{ID: 3, Enabled: true, Action: ActionInclude, Scheme: "HTTPS", HostPattern: "Api.Example.Test"},
			target: Target{Scheme: "https", Host: "api.example.test", Path: "/"},
			match:  true,
		},
		{
			name:   "wildcard matches subdomain",
			rule:   Rule{ID: 4, Enabled: true, Action: ActionInclude, HostPattern: "*.example.test"},
			target: Target{Scheme: "https", Host: "api.example.test", Path: "/"},
			match:  true,
		},
		{
			name:   "wildcard does not match apex",
			rule:   Rule{ID: 5, Enabled: true, Action: ActionInclude, HostPattern: "*.example.test"},
			target: Target{Scheme: "https", Host: "example.test", Path: "/"},
			match:  false,
		},
		{
			name:   "unicode target matches ascii idna rule",
			rule:   Rule{ID: 6, Enabled: true, Action: ActionInclude, HostPattern: "xn--bcher-kva.example"},
			target: Target{Scheme: "https", Host: "bücher.example", Path: "/"},
			match:  true,
		},
		{
			name:   "http default port matches",
			rule:   Rule{ID: 7, Enabled: true, Action: ActionInclude, Scheme: "http", HostPattern: "example.test", Port: 80},
			target: Target{Scheme: "http", Host: "example.test", Path: "/"},
			match:  true,
		},
		{
			name:   "https default port matches",
			rule:   Rule{ID: 8, Enabled: true, Action: ActionInclude, Scheme: "https", HostPattern: "example.test", Port: 443},
			target: Target{Scheme: "https", Host: "example.test:443", Path: "/"},
			match:  true,
		},
		{
			name:   "explicit non default port matches",
			rule:   Rule{ID: 9, Enabled: true, Action: ActionInclude, HostPattern: "example.test", Port: 8443},
			target: Target{Scheme: "https", Host: "example.test:8443", Path: "/"},
			match:  true,
		},
		{
			name:   "explicit non default port rejects default port",
			rule:   Rule{ID: 10, Enabled: true, Action: ActionInclude, HostPattern: "example.test", Port: 8443},
			target: Target{Scheme: "https", Host: "example.test", Path: "/"},
			match:  false,
		},
		{
			name:   "ipv4 host matches",
			rule:   Rule{ID: 11, Enabled: true, Action: ActionInclude, HostPattern: "192.0.2.10"},
			target: Target{Scheme: "http", Host: "192.0.2.10:80", Path: "/"},
			match:  true,
		},
		{
			name:   "bracketed ipv6 host matches",
			rule:   Rule{ID: 12, Enabled: true, Action: ActionInclude, HostPattern: "2001:db8::1"},
			target: Target{Scheme: "https", Host: "[2001:db8::1]:443", Path: "/"},
			match:  true,
		},
		{
			name:   "root path matches descendants",
			rule:   Rule{ID: 13, Enabled: true, Action: ActionInclude, HostPattern: "example.test", PathPrefix: "/"},
			target: Target{Scheme: "https", Host: "example.test", Path: "/nested/path"},
			match:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rules, err := Compile(1, []Rule{test.rule})
			if err != nil {
				t.Fatal(err)
			}
			if got := rules.Classify(test.target).InScope; got != test.match {
				t.Fatalf("Classify(%#v).InScope = %t, want %t", test.target, got, test.match)
			}
		})
	}
}

func TestCompileRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
	}{
		{name: "unsupported scheme", rule: Rule{Action: ActionInclude, Scheme: "ftp", HostPattern: "example.test"}},
		{name: "empty host pattern", rule: Rule{Action: ActionInclude, HostPattern: ""}},
		{name: "wildcard only at leading label", rule: Rule{Action: ActionInclude, HostPattern: "api.*.example.test"}},
		{name: "negative port", rule: Rule{Action: ActionInclude, HostPattern: "example.test", Port: -1}},
		{name: "port too large", rule: Rule{Action: ActionInclude, HostPattern: "example.test", Port: 65536}},
		{name: "path must begin with slash", rule: Rule{Action: ActionInclude, HostPattern: "example.test", PathPrefix: "api"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Compile(1, []Rule{test.rule}); err == nil {
				t.Fatal("Compile succeeded")
			}
		})
	}
}

func TestRuleSetDoesNotExposeMutableCompiledState(t *testing.T) {
	input := []Rule{{ID: 21, Enabled: true, Action: ActionInclude, HostPattern: "example.test"}}
	rules, err := Compile(11, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].HostPattern = "other.test"

	first := rules.Classify(Target{Scheme: "https", Host: "example.test", Path: "/"})
	if first.RuleID == nil {
		t.Fatal("first decision did not include a rule ID")
	}
	*first.RuleID = 999
	second := rules.Classify(Target{Scheme: "https", Host: "example.test", Path: "/"})
	if !second.InScope || second.RuleID == nil || *second.RuleID != 21 || second.Version != 11 {
		t.Fatalf("second decision = %#v", second)
	}
}
