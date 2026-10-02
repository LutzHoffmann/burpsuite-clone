package reports

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

var activeCheckTemplate = template.Must(template.New("active-checks").Funcs(template.FuncMap{"isRedirect": func(source string) bool { return strings.HasPrefix(source, "redirect_") }, "isCORS": func(source string) bool { return source == "cors" }}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Active checks #{{.ID}}</title>
<style>body{font:16px/1.5 Georgia,serif;max-width:1100px;margin:3rem auto;padding:0 1.5rem;color:#17232a}h1{font-size:2rem}p{max-width:75ch}table{width:100%;border-collapse:collapse;margin-top:2rem}th,td{padding:.65rem;text-align:left;border-bottom:1px solid #b8c8c8;vertical-align:top;overflow-wrap:anywhere}th{background:#e7efee}aside{padding:1rem;background:#f0f4e7;border-left:4px solid #687b38}@media print{body{margin:0;padding:0}}</style></head><body>
<h1>Active checks #{{.ID}}</h1><p>Crawl #{{.CrawlID}} · {{.State}} · {{.ObservationCount}} observations</p>
<aside>These are bounded GET observations, not a confirmed vulnerability. Reflected markers do not prove XSS or injection; external redirect and CORS observations do not prove exploitability. Partial or ambiguous responses require manual review.</aside>
<table><thead><tr><th>Input</th><th>URL</th><th>Status</th><th>Observation</th></tr></thead><tbody>
{{range .Observations}}<tr><td>{{.Source}}: {{.Parameter}}</td><td>{{.URL}}</td><td>{{.Status}}</td><td>{{if .Error}}{{.Error}}{{else if isRedirect .Source}}{{if .Found}}External redirect observed (not confirmed vulnerability){{else}}No exact external redirect observed{{end}}{{else if isCORS .Source}}{{if .Found}}Credentialed CORS origin reflection observed (not confirmed vulnerability){{else}}No credentialed CORS origin reflection observed{{end}}{{else if .Found}}Exact marker reflected in {{.Context}}{{if .Partial}} (partial response){{end}}{{else}}No exact marker observed{{end}}</td></tr>{{end}}
</tbody></table><p>Generated from locally stored metadata. No response bodies, marker values, credentials, or original query values are included.</p>
</body></html>`))

func ActiveCheckHTML(run store.ActiveCheckRun) ([]byte, error) {
	var output bytes.Buffer
	if err := activeCheckTemplate.Execute(&output, run); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
