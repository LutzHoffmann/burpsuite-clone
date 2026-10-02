package crawl

import (
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	got, err := NormalizeURL("HTTPS://Example.TEST:443/a/../b?x=1#part")
	if err != nil || got != "https://example.test/b?x=1" {
		t.Fatalf("normalized=%q error=%v", got, err)
	}
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,hi", "https://u:p@example.test/", "http://example.test/%zz", strings.Repeat("x", 4097)} {
		if _, err := NormalizeURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestExtractHTML(t *testing.T) {
	body := []byte(`<a href="/next#frag">next</a><a href="https://example.test:443/next">dup</a><a href="javascript:alert(1)">bad</a><form action="/submit" method="post"><input name="token" type="hidden" value="secret"><input name="q"><input value="unnamed"></form>`)
	links, forms, err := ExtractHTML("https://example.test/start", body)
	if err != nil || len(links) != 1 || links[0] != "https://example.test/next" {
		t.Fatalf("links=%v error=%v", links, err)
	}
	if len(forms) != 1 || forms[0].ActionURL != "https://example.test/submit" || forms[0].Method != "POST" || len(forms[0].Fields) != 2 {
		t.Fatalf("forms=%+v", forms)
	}
	if forms[0].Fields[0].Name != "token" || forms[0].Fields[0].Type != "hidden" || forms[0].Fields[1].Name != "q" {
		t.Fatalf("fields=%+v", forms[0].Fields)
	}
}
