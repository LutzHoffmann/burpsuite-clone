package intercept

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestReplacementMatchesStandardTemplateSemantics(t *testing.T) {
	for _, template := range []string{"$1é", "${1}é", "$name界", "${name}", "${bad$1}", "$$$1", "$1_", "$", "${", "$01"} {
		r := ReplacementRule{Pattern: `(?P<name>a)`, Replacement: template, Regex: true}
		want := regexp.MustCompile(r.Pattern).ReplaceAllString("a a", template)
		got, _, err := r.Replace("a a", 100)
		if err != nil || got != want {
			t.Errorf("template %q: got %q err=%v want %q", template, got, err, want)
		}
	}
}

func TestReplacementBoundsMatchMetadata(t *testing.T) {
	r := ReplacementRule{Pattern: "a", Replacement: "", Regex: true}
	if _, _, err := r.Replace(strings.Repeat("a", 10001), 20000); err == nil {
		t.Fatal("excessive match metadata accepted")
	}
	r = ReplacementRule{ID: "captures", Direction: "request", Target: "body", Pattern: strings.Repeat("()", 65), Regex: true}
	if ValidateState(ControllerState{ReplacementRules: []ReplacementRule{r}}) == nil {
		t.Fatal("excessive capture metadata accepted")
	}
}

func TestCloneItemRetainsAndIsolatesHeaders(t *testing.T) {
	original := Item{Headers: map[string][]string{"X-Test": {"one", "two"}}, Body: []byte("abc")}
	cloned := cloneItem(original)
	if len(cloned.Headers["X-Test"]) != 2 {
		t.Fatalf("headers lost: %#v", cloned)
	}
	cloned.Headers["X-Test"][0] = "changed"
	cloned.Body[0] = 'z'
	if original.Headers["X-Test"][0] != "one" || string(original.Body) != "abc" {
		t.Fatal("clone aliases original")
	}
}

func TestReplacementBoundsAndCaptures(t *testing.T) {
	r := ReplacementRule{Pattern: `(?P<name>foo)-(\d+)`, Replacement: `${name}:$2:$$`, Regex: true}
	got, changed, err := r.Replace("foo-12 foo-3", 100)
	if err != nil || !changed || got != "foo:12:$ foo:3:$" {
		t.Fatalf("%q %v %v", got, changed, err)
	}
	if _, _, err := r.Replace("foo-12 foo-3", 2); err == nil {
		t.Fatal("unbounded output")
	}
	r = ReplacementRule{Pattern: "a", Replacement: strings.Repeat("b", 4096)}
	if _, _, err := r.Replace(strings.Repeat("a", 100), 100); err == nil {
		t.Fatal("literal expansion not bounded")
	}
}

func TestValidateStateRejectsInvalidRules(t *testing.T) {
	base := ReplacementRule{ID: "a", Direction: "request", Target: "body", Pattern: "x", Replacement: "y", Enabled: true}
	for _, mutate := range []func(*ReplacementRule){
		func(r *ReplacementRule) { r.Regex = true; r.Pattern = "[" },
		func(r *ReplacementRule) { r.Direction = "response"; r.Target = "url" },
		func(r *ReplacementRule) { r.Target = "header"; r.Header = "Content-Encoding" },
		func(r *ReplacementRule) { r.Target = "header"; r.Header = "Content-Length" },
		func(r *ReplacementRule) { r.Target = "header"; r.Header = "bad\r\n" },
		func(r *ReplacementRule) { r.Pattern = strings.Repeat("x", 4097) },
	} {
		r := base
		mutate(&r)
		if ValidateState(ControllerState{ReplacementRules: []ReplacementRule{r}}) == nil {
			t.Fatalf("accepted %#v", r)
		}
	}
	if ValidateState(ControllerState{ReplacementRules: []ReplacementRule{base, base}}) == nil {
		t.Fatal("duplicate ID")
	}
	if ValidateState(ControllerState{Rules: make([]Rule, 101)}) == nil {
		t.Fatal("too many rules")
	}
}

func TestQueuesBoundedAndCancellationReleases(t *testing.T) {
	q := NewQueue(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 100)
	for i := 0; i < 100; i++ {
		go func(i int) { _, err := q.Enqueue(ctx, Item{ID: fmt.Sprint(i)}); done <- err }(i)
	}
	waitForQueueLength(t, q, 100)
	if _, err := q.Enqueue(ctx, Item{ID: "overflow"}); err == nil {
		t.Fatal("capacity not enforced")
	}
	cancel()
	for i := 0; i < 100; i++ {
		if err := <-done; err == nil {
			t.Fatal("expected cancellation")
		}
	}
	if len(q.List()) != 0 {
		t.Fatal("queue leaked")
	}
}

func TestControllerDefaultsAndDefensiveState(t *testing.T) {
	c := NewController(NewQueue(23*time.Second), false, nil)
	if c.ResponseQueue() == c.Queue() || c.ResponseQueue().timeout != 23*time.Second {
		t.Fatal("response queue contract")
	}
	state := c.State()
	if state.ResponseEnabled || len(state.ResponseRules) != 1 || !state.ResponseRules[0].Enabled {
		t.Fatal("defaults")
	}
	state.ResponseRules[0].Enabled = false
	if !c.State().ResponseRules[0].Enabled {
		t.Fatal("state aliases")
	}
	state.ReplacementRules = []ReplacementRule{{ID: "invalid"}}
	c.Update(state)
	if !c.State().ResponseRules[0].Enabled {
		t.Fatal("invalid update activated")
	}
}
