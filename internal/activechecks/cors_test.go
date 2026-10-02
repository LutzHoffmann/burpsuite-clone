package activechecks

import (
	"net/http"
	"testing"
)

func TestCredentialedCORSRequiresExactOriginAndCredentials(t *testing.T) {
	origin := "https://nonce.cors-check.invalid"
	if !credentialedCORS(http.Header{"Access-Control-Allow-Origin": {origin}, "Access-Control-Allow-Credentials": {"true"}}, origin) {
		t.Fatal("exact credentialed origin not detected")
	}
	for _, headers := range []http.Header{
		{"Access-Control-Allow-Origin": {"*"}, "Access-Control-Allow-Credentials": {"true"}},
		{"Access-Control-Allow-Origin": {origin}, "Access-Control-Allow-Credentials": {"false"}},
		{"Access-Control-Allow-Origin": {"https://other.test"}, "Access-Control-Allow-Credentials": {"true"}},
	} {
		if credentialedCORS(headers, origin) {
			t.Fatalf("false positive: %+v", headers)
		}
	}
}
