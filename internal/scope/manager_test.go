package scope

import (
	"sync"
	"testing"
)

func TestManagerReplacePublishesWholeRuleSet(t *testing.T) {
	initial, _ := Compile(1, nil)
	manager := NewManager(initial)
	next, _ := Compile(2, []Rule{{ID: 9, Enabled: true, Action: ActionInclude, HostPattern: "example.test"}})
	manager.Replace(next)
	decision := manager.Current().Classify(Target{Scheme: "https", Host: "example.test", Path: "/"})
	if !decision.InScope || decision.Version != 2 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestManagerConcurrentReplacePublishesOnlyCompleteRuleSets(t *testing.T) {
	initial, err := Compile(1, []Rule{{ID: 1, Enabled: true, Action: ActionInclude, HostPattern: "example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(initial)

	start := make(chan struct{})
	var readers sync.WaitGroup
	for range 100 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for range 100 {
				decision := manager.Current().Classify(Target{Scheme: "https", Host: "example.test", Path: "/"})
				if !decision.InScope || decision.RuleID == nil || decision.Version < 1 || decision.Version > 20 || *decision.RuleID != decision.Version {
					t.Errorf("incomplete published rule set decision = %#v", decision)
					return
				}
			}
		}()
	}

	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		<-start
		for version := int64(2); version <= 20; version++ {
			next, err := Compile(version, []Rule{{ID: version, Enabled: true, Action: ActionInclude, HostPattern: "example.test"}})
			if err != nil {
				t.Error(err)
				return
			}
			manager.Replace(next)
		}
	}()

	close(start)
	readers.Wait()
	writer.Wait()
}

func TestManagerNilInitialUsesEmptyRuleSetAndRejectsNilReplacement(t *testing.T) {
	manager := NewManager(nil)
	decision := manager.Current().Classify(Target{Scheme: "https", Host: "example.test", Path: "/"})
	if decision.InScope || decision.Version != 0 || decision.Reason != "no_include" {
		t.Fatalf("empty decision = %#v", decision)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("Replace(nil) did not panic")
		}
	}()
	manager.Replace(nil)
}
