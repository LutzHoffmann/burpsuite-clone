package crawl

import (
	"errors"
	"net"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Form struct {
	PageURL   string  `json:"pageUrl"`
	ActionURL string  `json:"actionUrl"`
	Method    string  `json:"method"`
	Fields    []Field `json:"fields"`
}

func NormalizeURL(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return "", errors.New("invalid URL length")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid crawl URL")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" && !((u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")) {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	u.Fragment = ""
	u.RawFragment = ""
	u.Path = path.Clean("/" + u.Path)
	if u.Path == "/." {
		u.Path = "/"
	}
	u.RawPath = ""
	return u.String(), nil
}

func ExtractHTML(base string, body []byte) ([]string, []Form, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, nil, err
	}
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, nil, err
	}
	var links []string
	var forms []Form
	seen := map[string]bool{}
	resolve := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		value, err := NormalizeURL(baseURL.ResolveReference(u).String())
		if err != nil {
			return ""
		}
		return value
	}
	var walk func(*html.Node, *Form)
	walk = func(n *html.Node, form *Form) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "a":
				if target := resolve(attr(n, "href")); target != "" && !seen[target] && attr(n, "href") != "" {
					seen[target] = true
					links = append(links, target)
				}
			case "form":
				action := base
				if raw := attr(n, "action"); raw != "" {
					action = resolve(raw)
				}
				method := strings.ToUpper(attr(n, "method"))
				if method == "" {
					method = "GET"
				}
				if action != "" && len(forms) < 100 {
					forms = append(forms, Form{PageURL: base, ActionURL: action, Method: method, Fields: []Field{}})
					form = &forms[len(forms)-1]
				}
			case "input", "select", "textarea":
				if form != nil && len(form.Fields) < 100 {
					name := attr(n, "name")
					if name != "" && len(name) <= 256 {
						typ := attr(n, "type")
						if typ == "" {
							typ = n.Data
						}
						if len(typ) <= 64 {
							form.Fields = append(form.Fields, Field{Name: name, Type: typ})
						}
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, form)
		}
	}
	walk(root, nil)
	return links, forms, nil
}

func attr(n *html.Node, key string) string {
	for _, item := range n.Attr {
		if item.Key == key {
			return item.Val
		}
	}
	return ""
}
