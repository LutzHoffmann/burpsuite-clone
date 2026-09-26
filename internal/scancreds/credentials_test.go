package scancreds

import "testing"

func TestCredentialsHeaders(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input Credentials
		valid bool
	}{
		{"empty", Credentials{}, true},
		{"cookie", Credentials{Cookie: "sid=abc"}, true},
		{"bearer", Credentials{Authorization: "Bearer secret"}, true},
		{"newline", Credentials{Cookie: "sid=x\r\nHost: evil.test"}, false},
		{"control", Credentials{Authorization: "Bearer \x00secret"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err == nil {
				headers := tc.input.Headers("Scanner/1")
				if headers["User-Agent"][0] != "Scanner/1" {
					t.Fatal(headers)
				}
				if tc.input.Cookie != "" && headers["Cookie"][0] != tc.input.Cookie {
					t.Fatal(headers)
				}
			}
		})
	}
}
