package activechecks

import "testing"

func TestClassify(t *testing.T) {
	marker := "scan-012345"
	for _, tc := range []struct {
		name, body, contentType, want string
		found, partial                bool
	}{
		{"html text", "<p>scan-012345</p>", "text/html", "html_text", true, false},
		{"attribute", `<a title="scan-012345">x</a>`, "text/html", "html_attribute", true, false},
		{"script", `<script>const x='scan-012345'</script>`, "text/html", "raw_text", true, false},
		{"plain", "scan-012345", "text/plain", "plain_text", true, false},
		{"altered", "scan-01234", "text/html", "unknown", false, false},
		{"ambiguous", `<p>scan-012345</p><a title="scan-012345">x</a>`, "text/html", "unknown", true, false},
		{"truncated", `<p>scan-012345`, "text/html", "unknown", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify([]byte(tc.body), tc.contentType, marker, tc.partial)
			if got.Context != tc.want || got.Found != tc.found || got.Partial != tc.partial {
				t.Fatalf("got=%+v", got)
			}
		})
	}
}
