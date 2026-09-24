package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPassiveFindingsGroupFilterAndSnapshot(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	save := func(host string, inScope bool, headers map[string][]string) int64 {
		e := &Exchange{Method: "GET", Scheme: "https", Host: host, Path: "/page", Status: 200, MIMEType: "text/html", InScope: inScope, StartedAt: time.Now(), Response: ResponseData{Headers: headers}}
		if err := s.SaveExchange(ctx, e); err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	save("example.test", true, map[string][]string{"Set-Cookie": {"sid=private; HttpOnly", "sid=other; HttpOnly"}})
	latest := save("example.test", true, map[string][]string{"Set-Cookie": {"sid=secret; HttpOnly"}})
	save("outside.test", false, nil)
	page, err := s.ListPassiveFindings(ctx, FindingsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.SnapshotID != 3 || len(page.Items) != 3 {
		t.Fatalf("page=%+v", page)
	}
	for _, item := range page.Items {
		if item.Host != "example.test" || item.Count != 2 || item.LatestExchangeID != latest || strings.Contains(item.Subject, "secret") {
			t.Fatalf("item=%+v", item)
		}
	}
	filtered, err := s.ListPassiveFindings(ctx, FindingsRequest{Host: "example.test", Type: "cookie_secure_missing", SnapshotID: page.SnapshotID})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Subject != "sid" {
		t.Fatalf("filtered=%+v, %v", filtered, err)
	}
	save("new.test", true, nil)
	old, err := s.ListPassiveFindings(ctx, FindingsRequest{SnapshotID: page.SnapshotID})
	if err != nil || len(old.Items) != 3 {
		t.Fatalf("snapshot=%+v, %v", old, err)
	}
	fresh, err := s.ListPassiveFindings(ctx, FindingsRequest{})
	if err != nil || len(fresh.Items) != 5 {
		t.Fatalf("fresh=%+v, %v", fresh, err)
	}
}

func TestPassiveFindingsNoSensitiveHeaderValues(t *testing.T) {
	s := openTestStore(t)
	e := &Exchange{Method: "GET", Scheme: "https", Host: "example.test", Status: 200, MIMEType: "text/plain", InScope: true, StartedAt: time.Now(), Response: ResponseData{Headers: map[string][]string{"Set-Cookie": {"token=do-not-leak; HttpOnly"}}}}
	if err := s.SaveExchange(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPassiveFindings(context.Background(), FindingsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if strings.Contains(item.Subject, "do-not-leak") || strings.Contains(item.LatestPath, "do-not-leak") {
			t.Fatalf("secret exposed: %+v", item)
		}
	}
}

func TestPassiveFindingsPagesMoreThanHundredGroups(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for i := 0; i < 51; i++ {
		e := &Exchange{Method: "GET", Scheme: "https", Host: fmt.Sprintf("host-%03d.test", i), Status: 200, MIMEType: "text/html", InScope: true, StartedAt: time.Now()}
		if err := s.SaveExchange(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ListPassiveFindings(ctx, FindingsRequest{})
	if err != nil || len(first.Items) != 100 || first.NextOffset != 100 {
		t.Fatalf("first=%+v, %v", first, err)
	}
	second, err := s.ListPassiveFindings(ctx, FindingsRequest{Offset: first.NextOffset, SnapshotID: first.SnapshotID})
	if err != nil || len(second.Items) != 2 || second.NextOffset != 0 {
		t.Fatalf("second=%+v, %v", second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		key := item.Host + ":" + item.Type
		if seen[key] {
			t.Fatalf("duplicate group %s", key)
		}
		seen[key] = true
	}
}
