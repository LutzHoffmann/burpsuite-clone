package activechecks

import (
	"net/url"
	"testing"
)

func TestProbeTargetPreservesParameterNamesWithoutValues(t *testing.T) {
	target, err := probeTarget("https://example.test/search?q=private-value&lang=de&lang=en", "q", "scan-marker")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("q"); got != "scan-marker" {
		t.Fatalf("q=%q", got)
	}
	if got := u.Query()["lang"]; len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Fatalf("lang=%q", got)
	}
	if u.Fragment != "" || u.RawQuery == "" {
		t.Fatalf("unexpected target: %s", target)
	}
}

func TestProbeTargetAddsFormParameter(t *testing.T) {
	target, err := probeTarget("https://example.test/find?mode=private", "term", "scan-marker")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	if u.Query().Get("term") != "scan-marker" || u.Query().Get("mode") != "" {
		t.Fatalf("unexpected query: %s", u.RawQuery)
	}
}
