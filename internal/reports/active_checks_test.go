package reports

import (
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestActiveCheckHTMLIsSelfContainedAndEscaped(t *testing.T) {
	run := store.ActiveCheckRun{ID: 9, CrawlID: 3, State: "completed", ObservationCount: 1, Observations: []store.ActiveCheckObservation{{URL: "https://example.test/<script>alert(1)</script>", Source: "query", Parameter: `<img src=x onerror=alert(1)>`, Status: 200, Found: true, Context: "html_text"}}}
	output, err := ActiveCheckHTML(run)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	if strings.Contains(html, "<script>alert(1)</script>") || strings.Contains(html, `<img src=x onerror=alert(1)>`) {
		t.Fatal("unescaped observation")
	}
	if !strings.Contains(html, "not a confirmed vulnerability") || !strings.Contains(html, "Active checks #9") {
		t.Fatalf("missing report context: %s", html)
	}
}

func TestActiveCheckHTMLLabelsRedirectObservations(t *testing.T) {
	run := store.ActiveCheckRun{ID: 10, CrawlID: 4, State: "completed", Observations: []store.ActiveCheckObservation{{URL: "https://example.test/go", Source: "redirect_query", Parameter: "next", Status: 302, Found: true, Context: "redirect_location"}}}
	output, err := ActiveCheckHTML(run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "External redirect observed") || strings.Contains(string(output), "Exact marker reflected in redirect_location") {
		t.Fatalf("redirect report mislabeled: %s", output)
	}
}
