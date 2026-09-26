package activechecks

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

type Observation struct {
	Found   bool   `json:"found"`
	Context string `json:"context"`
	Partial bool   `json:"partial"`
}

func Classify(body []byte, contentType, marker string, truncated bool) Observation {
	result := Observation{Found: marker != "" && bytes.Contains(body, []byte(marker)), Context: "unknown", Partial: truncated}
	if !result.Found || truncated {
		return result
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "text/html") {
		result.Context = "plain_text"
		return result
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return result
	}
	contexts := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode && strings.Contains(n.Data, marker) {
			kind := "html_text"
			if n.Parent != nil && (n.Parent.Data == "script" || n.Parent.Data == "style") {
				kind = "raw_text"
			}
			contexts[kind] = true
		}
		if n.Type == html.ElementNode {
			for _, attr := range n.Attr {
				if strings.Contains(attr.Val, marker) {
					contexts["html_attribute"] = true
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if len(contexts) == 1 {
		for kind := range contexts {
			result.Context = kind
		}
	}
	return result
}
