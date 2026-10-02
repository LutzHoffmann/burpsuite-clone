package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/intercept"
)

func TestForwardEditLimits(t *testing.T) {
	const limit = 24
	for _, phase := range []string{"request", "response"} {
		for _, tc := range []struct {
			name, body, padding string
			status              int
		}{
			{"at-limit", strings.Repeat("a", limit), "", 204},
			{"escaped-at-limit", strings.Repeat("\x00", limit), "", 204},
			{"utf8-at-limit", strings.Repeat("\u20ac", limit/3), "", 204},
			{"over-limit", strings.Repeat("a", limit+1), "", 413},
			{"utf8-over-limit", strings.Repeat("\u20ac", limit/3) + "a", "", 413},
			{"huge-envelope", "", strings.Repeat(" ", 6*limit+(1<<20)+1), 413},
			{"huge-metadata", "", "", 413},
		} {
			t.Run(phase+"/"+tc.name, func(t *testing.T) {
				c := intercept.NewController(intercept.NewQueue(time.Minute), true, nil)
				s := NewServer(Config{Intercept: c, MaxBodyBytes: limit})
				q, path := c.Queue(), "/api/intercept/edit/forward"
				edit := map[string]interface{}{"method": "POST", "url": "http://example.test/", "body": tc.body}
				if phase == "response" {
					q, path = c.ResponseQueue(), "/api/intercept/response/edit/forward"
					edit = map[string]interface{}{"statusCode": 200, "body": tc.body}
				}
				ctx, cancel := context.WithCancel(context.Background())
				if tc.name == "huge-metadata" {
					edit["headers"] = map[string][]string{"X-Large": {strings.Repeat("a", 6*limit+(1<<20)+1)}}
				}
				defer cancel()
				result := make(chan intercept.Decision, 1)
				go func() {
					d, _ := q.Enqueue(ctx, intercept.Item{ID: "edit", Phase: phase, Method: "POST", StatusCode: 200, BodyEditable: true, Body: []byte(tc.body)})
					result <- d
				}()
				waitForAPIQueue(t, q)
				data, err := json.Marshal(edit)
				if err != nil {
					t.Fatal(err)
				}
				w := interceptAPICall(s, http.MethodPost, path, string(data)+tc.padding)
				if w.Code != tc.status {
					t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body)
				}
				if tc.status != 204 {
					if _, ok := q.Get("edit"); !ok {
						t.Fatal("rejected edit consumed pending item")
					}
					return
				}
				select {
				case d := <-result:
					if d.Action != intercept.ActionForward || !d.Edit.BodySet || string(d.Edit.Body) != tc.body {
						t.Fatalf("unexpected decision: %+v", d)
					}
				case <-time.After(time.Second):
					t.Fatal("not forwarded")
				}
			})
		}
	}
}

func TestRegularJSONLimitUnchanged(t *testing.T) {
	s := NewServer(Config{Intercept: intercept.NewController(nil, false, nil), MaxBodyBytes: 24})
	w := interceptAPICall(s, http.MethodPut, "/api/intercept/config", `{"enabled":true,"rules":[]}`)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
}

func TestForwardEditLimitOverflow(t *testing.T) {
	for _, limit := range []int64{(math.MaxInt64 - (1 << 20)) / 6, (math.MaxInt64-(1<<20))/6 + 1, math.MaxInt64} {
		s := NewServer(Config{Intercept: intercept.NewController(nil, false, nil), MaxBodyBytes: limit})
		r := httptest.NewRequest(http.MethodPost, "http://localhost/", strings.NewReader(`{"body":"ok"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		var edit responseEditDTO
		if err := s.decodeEditJSON(w, r, &edit, &edit.Body); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if edit.Body != "ok" {
			t.Fatal("body not decoded")
		}
	}
}
