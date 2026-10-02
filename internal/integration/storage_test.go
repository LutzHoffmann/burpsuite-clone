package integration_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestFullCaptureBudgetPreservesProxyTraffic(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("https=%v", secure), func(t *testing.T) {
			h := newHarness(t, secure, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, "traffic still works")
			}))
			if got := await(t, h.request("GET", "/allowed/retained", "")); got.body != "traffic still works" {
				t.Fatal(got)
			}
			_ = h.waitHistory(1)
			h.json("PUT", "/api/storage", map[string]any{"limitBytes": 1}, 200, nil)
			for i := 0; i < 2; i++ {
				got := await(t, h.request("GET", "/allowed/skipped", ""))
				if got.status != 200 || got.body != "traffic still works" {
					t.Fatalf("quota changed traffic: %#v", got)
				}
			}
			var status struct {
				Paused  bool  `json:"paused"`
				Skipped int64 `json:"skippedRecords"`
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				h.json("GET", "/api/storage", nil, 200, &status)
				if status.Skipped == 2 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !status.Paused || status.Skipped != 2 {
				t.Fatalf("quota state: %#v", status)
			}
			var history []struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}
			h.json("GET", "/api/history", nil, 200, &history)
			if len(history) != 1 || history[0].Path != "/allowed/retained" {
				t.Fatalf("existing capture changed or skipped capture saved: %#v", history)
			}
			h.json("PUT", "/api/storage", map[string]any{"limitBytes": 1 << 30}, 200, nil)
			_ = await(t, h.request("GET", "/allowed/resumed", ""))
			_ = h.waitHistory(2)
		})
	}
}
