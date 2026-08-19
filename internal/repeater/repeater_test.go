package repeater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendReturnsCapturedResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			t.Fatalf("X-Test = %q", r.Header.Get("X-Test"))
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("repeater response"))
	}))
	defer target.Close()

	service := NewService(http.DefaultTransport, 2048)
	result, err := service.Send(context.Background(), SendRequest{
		Method: "POST",
		URL:    target.URL + "/repeat",
		Headers: map[string][]string{
			"X-Test": {"yes"},
		},
		Body: []byte("request"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != 200 {
		t.Fatalf("Status = %d", result.Status)
	}
	if string(result.Body) != "repeater response" {
		t.Fatalf("Body = %q", result.Body)
	}
}
