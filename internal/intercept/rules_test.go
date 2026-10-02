package intercept

import (
	"context"
	"testing"
	"time"
)

func TestMatchesHostPathAndMethod(t *testing.T) {
	rule := Rule{Enabled: true, Method: "POST", HostContains: "example", PathContains: "/login"}
	req := MatchRequest{Method: "POST", Host: "app.example.test", Path: "/login"}
	if !Matches(rule, req) {
		t.Fatal("expected rule to match")
	}
	req.Path = "/profile"
	if Matches(rule, req) {
		t.Fatal("expected rule not to match")
	}
}

func TestQueueForwardReturnsEditedRequest(t *testing.T) {
	q := NewQueue(2 * time.Second)
	done := make(chan Decision, 1)
	go func() {
		decision, err := q.Enqueue(context.Background(), Item{
			ID:     "one",
			Method: "GET",
			URL:    "https://example.test/",
			Headers: map[string][]string{
				"User-Agent": {"test"},
			},
		})
		if err != nil {
			t.Error(err)
			return
		}
		done <- decision
	}()

	waitForQueueLength(t, q, 1)
	if err := q.Forward("one", RequestEdit{Body: []byte("edited")}); err != nil {
		t.Fatal(err)
	}

	decision := <-done
	if decision.Action != ActionForward {
		t.Fatalf("Action = %s", decision.Action)
	}
	if string(decision.Edit.Body) != "edited" {
		t.Fatalf("Body = %q", decision.Edit.Body)
	}
}

func waitForQueueLength(t *testing.T, q *Queue, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(q.List()) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("queue length did not reach %d", want)
}
