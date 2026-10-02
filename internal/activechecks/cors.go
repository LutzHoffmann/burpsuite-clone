package activechecks

import (
	"net/http"
	"strings"
)

func credentialedCORS(headers http.Header, origin string) bool {
	allowed := headers.Values("Access-Control-Allow-Origin")
	credentials := headers.Values("Access-Control-Allow-Credentials")
	return len(allowed) == 1 && len(credentials) == 1 && strings.TrimSpace(allowed[0]) == origin && strings.EqualFold(strings.TrimSpace(credentials[0]), "true")
}
