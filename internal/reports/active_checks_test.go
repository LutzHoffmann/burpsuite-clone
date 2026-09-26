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
