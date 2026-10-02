package target

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestAnalyzeExtractsValueFreeParameters(t *testing.T) {
	exchange := &store.Exchange{
		ID: 41, Method: "post", Scheme: "HTTPS", Host: "EXAMPLE.TEST:443", Path: "/users", Query: "page=2&page=3",
		StartedAt: time.Unix(10, 0).UTC(), Status: 201, MIMEType: "application/json",
		Request: store.RequestData{
			Headers: http.Header{"Content-Type": {"application/json"}, "Cookie": {"session=secret"}},
			Body:    []byte(`{"user":{"email":"a@example.test","admin":false},"items":[{"id":7}]}`),
		},
	}
	observation, err := Analyze(exchange, Limits{MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Key != (store.TargetEndpointKey{Scheme: "https", Host: "example.test", Port: 443, Path: "/users", Method: "POST"}) {
		t.Fatalf("key = %#v", observation.Key)
	}
	want := []store.TargetParameter{
		{Location: "query", Name: "page", ValueType: "string"},
		{Location: "json", Name: "items[].id", ValueType: "number"},
		{Location: "json", Name: "user.admin", ValueType: "boolean"},
		{Location: "json", Name: "user.email", ValueType: "string"},
		{Location: "cookie", Name: "session", ValueType: "string"},
	}
	slices.SortFunc(observation.Parameters, compareTargetParameter)
	slices.SortFunc(want, compareTargetParameter)
	if !slices.Equal(observation.Parameters, want) {
		t.Fatalf("parameters = %#v, want %#v", observation.Parameters, want)
	}
}

func TestAnalyzeExtractsURLencodedFormParameters(t *testing.T) {
	observation := analyze(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/login", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/x-www-form-urlencoded; charset=utf-8"}}, Body: []byte("email=alice%40example.test&role=admin&role=user")},
	})
	assertParameters(t, observation.Parameters, []store.TargetParameter{
		{Location: "form", Name: "email", ValueType: "string"},
		{Location: "form", Name: "role", ValueType: "string"},
	})
}

func TestAnalyzeExtractsNonFileMultipartParameters(t *testing.T) {
	body := "--x\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nprivate title\r\n" +
		"--x\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"secret.txt\"\r\nContent-Type: text/plain\r\n\r\nsecret file\r\n" +
		"--x\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nagain\r\n--x--\r\n"
	observation := analyze(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/upload", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"multipart/form-data; boundary=x"}}, Body: []byte(body)},
	})
	assertParameters(t, observation.Parameters, []store.TargetParameter{{Location: "multipart", Name: "title", ValueType: "string"}})
}

func TestAnalyzeExcludesMultipartFilePartWithEmptyFilename(t *testing.T) {
	body := "--x\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"\"\r\nContent-Type: text/plain\r\n\r\nsecret file\r\n--x--\r\n"
	observation := analyze(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/upload", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"multipart/form-data; boundary=x"}}, Body: []byte(body)},
	})
	if len(observation.Parameters) != 0 {
		t.Fatalf("parameters = %#v, want none", observation.Parameters)
	}
}

func TestAnalyzeDiagnosesMalformedMultipartDispositionWithoutPartialParameters(t *testing.T) {
	body := "--x\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nprivate title\r\n" +
		"--x\r\nContent-Disposition: form-data; name=\"broken\r\n\r\nsecret\r\n--x--\r\n"
	observation := analyze(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/upload", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"multipart/form-data; boundary=x"}}, Body: []byte(body)},
	})
	if observation.ParseDiagnostic != "multipart_malformed" {
		t.Fatalf("diagnostic = %q, want multipart_malformed", observation.ParseDiagnostic)
	}
	if len(observation.Parameters) != 0 {
		t.Fatalf("parameters = %#v, want none", observation.Parameters)
	}
}

func TestAnalyzeExtractsJSONCompositeTypesAndArrayPaths(t *testing.T) {
	observation := analyze(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/payload", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"emptyObject":{},"emptyArray":[],"nothing":null,"items":[{"id":7},{"id":8}],"nested":{"name":"alice"}}`)},
	})
	assertParameters(t, observation.Parameters, []store.TargetParameter{
		{Location: "json", Name: "emptyArray", ValueType: "array"},
		{Location: "json", Name: "emptyObject", ValueType: "object"},
		{Location: "json", Name: "items[].id", ValueType: "number"},
		{Location: "json", Name: "nested.name", ValueType: "string"},
		{Location: "json", Name: "nothing", ValueType: "null"},
	})
}

func TestAnalyzeStopsQueryTokenizationAtTotalFieldLimit(t *testing.T) {
	observation := analyzeWithLimits(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/", Method: "GET",
		Query:   "first=query-secret-1&second=query-secret-2&third=query-secret-3",
		Request: store.RequestData{Headers: http.Header{"Cookie": {"session=cookie-secret"}}},
	}, Limits{MaxJSONDepth: 16, MaxFields: 2, MaxMultipartFields: 100})
	assertParameters(t, observation.Parameters, []store.TargetParameter{
		{Location: "query", Name: "first", ValueType: "string"},
		{Location: "query", Name: "second", ValueType: "string"},
	})
	assertValueFreeDiagnostic(t, observation, "query_field_limit_exceeded", []string{
		"query-secret-1", "query-secret-2", "query-secret-3", "cookie-secret",
	})
}

func TestAnalyzeStopsCookieTokenizationAtRemainingFieldLimit(t *testing.T) {
	observation := analyzeWithLimits(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/", Method: "GET", Query: "page=query-secret",
		Request: store.RequestData{Headers: http.Header{"Cookie": {"first=cookie-secret-1; second=cookie-secret-2; third=cookie-secret-3"}}},
	}, Limits{MaxJSONDepth: 16, MaxFields: 3, MaxMultipartFields: 100})
	assertParameters(t, observation.Parameters, []store.TargetParameter{
		{Location: "query", Name: "page", ValueType: "string"},
		{Location: "cookie", Name: "first", ValueType: "string"},
		{Location: "cookie", Name: "second", ValueType: "string"},
	})
	assertValueFreeDiagnostic(t, observation, "cookie_field_limit_exceeded", []string{
		"query-secret", "cookie-secret-1", "cookie-secret-2", "cookie-secret-3",
	})
}

func TestAnalyzeSharesTotalFieldLimitWithBodyParameters(t *testing.T) {
	observation := analyzeWithLimits(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/", Method: "POST", Query: "page=1",
		Request: store.RequestData{
			Headers: http.Header{"Content-Type": {"application/json"}, "Cookie": {"session=secret"}},
			Body:    []byte(`{"first":1,"second":2}`),
		},
	}, Limits{MaxJSONDepth: 16, MaxFields: 3, MaxMultipartFields: 100})
	assertParameters(t, observation.Parameters, []store.TargetParameter{
		{Location: "query", Name: "page", ValueType: "string"},
		{Location: "cookie", Name: "session", ValueType: "string"},
	})
	if observation.ParseDiagnostic != "json_field_limit_exceeded" {
		t.Fatalf("diagnostic = %q, want json_field_limit_exceeded", observation.ParseDiagnostic)
	}
}

func TestAnalyzeNonPositiveMaxFieldsDisablesAllParameterParsing(t *testing.T) {
	for _, maxFields := range []int{0, -1} {
		t.Run(strconv.Itoa(maxFields), func(t *testing.T) {
			observation := analyzeWithLimits(t, &store.Exchange{
				Scheme: "http", Host: "example.test", Path: "/", Method: "POST", Query: "page=secret",
				Request: store.RequestData{
					Headers: http.Header{"Content-Type": {"application/json"}, "Cookie": {"session=secret"}},
					Body:    []byte(`{"token":"secret"}`),
				},
			}, Limits{MaxJSONDepth: 16, MaxFields: maxFields, MaxMultipartFields: 100})
			if len(observation.Parameters) != 0 {
				t.Fatalf("parameters = %#v, want none", observation.Parameters)
			}
			if observation.ParseDiagnostic != "parameter_field_limit_exceeded" {
				t.Fatalf("diagnostic = %q, want parameter_field_limit_exceeded", observation.ParseDiagnostic)
			}
		})
	}
}

func TestAnalyzeEncodesJSONPathComponentsWithoutCollisions(t *testing.T) {
	observation := analyze(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/payload", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"a.b":1,"a":{"b":2},"items[]":3}`)},
	})
	assertParameters(t, observation.Parameters, []store.TargetParameter{
		{Location: "json", Name: `a\.b`, ValueType: "number"},
		{Location: "json", Name: "a.b", ValueType: "number"},
		{Location: "json", Name: `items\[\]`, ValueType: "number"},
	})
}

func TestAnalyzeBodyDiagnosticsAreFixedAndValueFree(t *testing.T) {
	tests := []struct {
		name       string
		exchange   *store.Exchange
		diagnostic string
	}{
		{
			name: "malformed json", diagnostic: "json_malformed",
			exchange: &store.Exchange{Scheme: "http", Host: "example.test", Path: "/", Method: "POST", Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"token":"secret",`)}},
		},
		{
			name: "unsupported mime", diagnostic: "body_unsupported_mime",
			exchange: &store.Exchange{Scheme: "http", Host: "example.test", Path: "/", Method: "POST", Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/xml"}}, Body: []byte("<token>secret</token>")}},
		},
		{
			name: "binary body", diagnostic: "body_binary",
			exchange: &store.Exchange{Scheme: "http", Host: "example.test", Path: "/", Method: "POST", Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte{0xff, 0x00, 0xfe}}},
		},
		{
			name: "truncated request", diagnostic: "body_truncated",
			exchange: &store.Exchange{Scheme: "http", Host: "example.test", Path: "/", Method: "POST", RequestTruncated: true, Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"token":"secret"}`)}},
		},
		{
			name: "compressed request", diagnostic: "body_compressed",
			exchange: &store.Exchange{Scheme: "http", Host: "example.test", Path: "/", Method: "POST", Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, Body: []byte(`{"token":"secret"}`)}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := analyze(t, test.exchange)
			if observation.ParseDiagnostic != test.diagnostic {
				t.Fatalf("diagnostic = %q, want %q", observation.ParseDiagnostic, test.diagnostic)
			}
			if len(observation.Parameters) != 0 {
				t.Fatalf("body parameters = %#v, want none", observation.Parameters)
			}
		})
	}
}

func TestAnalyzeStopsAtJSONDepthAndFieldLimits(t *testing.T) {
	depth := analyzeWithLimits(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"a":{"b":{"c":true}}}`)},
	}, Limits{MaxJSONDepth: 2, MaxFields: 100, MaxMultipartFields: 100})
	if depth.ParseDiagnostic != "json_depth_exceeded" || len(depth.Parameters) != 0 {
		t.Fatalf("depth observation = %#v", depth)
	}

	fields := analyzeWithLimits(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"one":1,"two":2}`)},
	}, Limits{MaxJSONDepth: 16, MaxFields: 1, MaxMultipartFields: 100})
	if fields.ParseDiagnostic != "json_field_limit_exceeded" || len(fields.Parameters) != 0 {
		t.Fatalf("field observation = %#v", fields)
	}
}

func TestAnalyzeStopsAtMultipartFieldLimit(t *testing.T) {
	body := "--x\r\nContent-Disposition: form-data; name=\"one\"\r\n\r\n1\r\n--x\r\nContent-Disposition: form-data; name=\"two\"\r\n\r\n2\r\n--x--\r\n"
	observation := analyzeWithLimits(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"multipart/form-data; boundary=x"}}, Body: []byte(body)},
	}, Limits{MaxJSONDepth: 16, MaxFields: 100, MaxMultipartFields: 1})
	if observation.ParseDiagnostic != "multipart_field_limit_exceeded" || len(observation.Parameters) != 0 {
		t.Fatalf("multipart observation = %#v", observation)
	}
}

func TestAnalyzeDiagnosesIncompleteMultipartBody(t *testing.T) {
	body := "--x\r\nContent-Disposition: form-data; name=\"token\"\r\n\r\nsecret\r\n"
	observation := analyze(t, &store.Exchange{
		Scheme: "http", Host: "example.test", Path: "/", Method: "POST",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"multipart/form-data; boundary=x"}}, Body: []byte(body)},
	})
	if observation.ParseDiagnostic != "multipart_malformed" || len(observation.Parameters) != 0 {
		t.Fatalf("multipart observation = %#v", observation)
	}
}

func TestAnalyzeSerializedObservationNeverContainsParameterValues(t *testing.T) {
	observation := analyze(t, &store.Exchange{
		Scheme: "https", Host: "example.test", Path: "/", Method: "POST", Query: "token=query-secret",
		Request: store.RequestData{Headers: http.Header{"Content-Type": {"application/json"}, "Cookie": {"session=cookie-secret"}}, Body: []byte(`{"token":"body-secret"}`)},
	})
	serialized, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"query-secret", "cookie-secret", "body-secret"} {
		if strings.Contains(string(serialized), secret) {
			t.Fatalf("serialized observation contains %q: %s", secret, serialized)
		}
	}
}

func FuzzAnalyzeNeverPanics(f *testing.F) {
	f.Add("application/json", `{"broken":`)
	f.Add("multipart/form-data; boundary=missing", "--wrong\r\n")
	f.Add("application/json", string([]byte{0xff, 0xfe, 0xfd}))
	f.Add("application/json", strings.Repeat("[", 64)+strings.Repeat("]", 64))
	f.Fuzz(func(t *testing.T, contentType, body string) {
		_, _ = Analyze(&store.Exchange{
			Scheme: "https", Host: "example.test", Path: "/", Method: "POST",
			Request: store.RequestData{Headers: http.Header{"Content-Type": {contentType}}, Body: []byte(body)},
		}, Limits{MaxJSONDepth: 16, MaxFields: 100, MaxMultipartFields: 10})
	})
}

func analyze(t *testing.T, exchange *store.Exchange) store.TargetObservation {
	t.Helper()
	return analyzeWithLimits(t, exchange, Limits{MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100})
}

func analyzeWithLimits(t *testing.T, exchange *store.Exchange, limits Limits) store.TargetObservation {
	t.Helper()
	observation, err := Analyze(exchange, limits)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func assertParameters(t *testing.T, got, want []store.TargetParameter) {
	t.Helper()
	slices.SortFunc(got, compareTargetParameter)
	slices.SortFunc(want, compareTargetParameter)
	if !slices.Equal(got, want) {
		t.Fatalf("parameters = %#v, want %#v", got, want)
	}
}

func assertValueFreeDiagnostic(t *testing.T, observation store.TargetObservation, want string, secrets []string) {
	t.Helper()
	if observation.ParseDiagnostic != want {
		t.Fatalf("diagnostic = %q, want %q", observation.ParseDiagnostic, want)
	}
	serialized, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(serialized), secret) {
			t.Fatalf("serialized observation contains %q: %s", secret, serialized)
		}
	}
}

func compareTargetParameter(a, b store.TargetParameter) int {
	if result := strings.Compare(a.Location, b.Location); result != 0 {
		return result
	}
	if result := strings.Compare(a.Name, b.Name); result != 0 {
		return result
	}
	return strings.Compare(a.ValueType, b.ValueType)
}
