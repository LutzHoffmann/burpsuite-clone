package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func historyPageStores(t *testing.T, test func(*testing.T, Store, HistoryPageStore)) {
	t.Helper()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			var s Store = NewMemoryForTests()
			if name == "sqlite" {
				db, err := OpenSQLite(filepath.Join(t.TempDir(), "history.db"))
				if err != nil {
					t.Fatal(err)
				}
				s = db
			}
			t.Cleanup(func() { s.Close() })
			test(t, s, s.(HistoryPageStore))
		})
	}
}

func saveHistoryPageExchange(t *testing.T, s Store, e Exchange) {
	t.Helper()
	e.StartedAt = time.Unix(100, 0).UTC()
	if err := s.SaveExchange(context.Background(), &e); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryPageBoundariesAndSnapshot(t *testing.T) {
	for _, count := range []int{0, 100, 101, 201} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			historyPageStores(t, func(t *testing.T, s Store, pages HistoryPageStore) {
				ctx := context.Background()
				for i := 0; i < count; i++ {
					saveHistoryPageExchange(t, s, Exchange{Method: "GET"})
				}
				first, err := pages.ListHistoryPage(ctx, HistoryPageRequest{})
				if err != nil {
					t.Fatal(err)
				}
				if first.Items == nil || first.SnapshotID != int64(count) {
					t.Fatalf("first page: %+v", first)
				}
				if len(first.Items) != min(count, 100) {
					t.Fatalf("length %d", len(first.Items))
				}
				if (first.NextBeforeID != 0) != (count > 100) {
					t.Fatalf("next cursor %d", first.NextBeforeID)
				}
				if count == 0 {
					return
				}
				saveHistoryPageExchange(t, s, Exchange{Method: "POST"})
				page := first
				wantID := int64(count)
				for {
					for _, item := range page.Items {
						if item.ID != wantID {
							t.Fatalf("id %d want %d", item.ID, wantID)
						}
						wantID--
					}
					if page.NextBeforeID == 0 {
						break
					}
					if page.NextBeforeID != page.Items[len(page.Items)-1].ID {
						t.Fatal("cursor not last returned ID")
					}
					page, err = pages.ListHistoryPage(ctx, HistoryPageRequest{BeforeID: page.NextBeforeID, SnapshotID: first.SnapshotID})
					if err != nil {
						t.Fatal(err)
					}
					if page.SnapshotID != first.SnapshotID || len(page.Items) > 100 {
						t.Fatalf("unstable page %+v", page)
					}
				}
				if wantID != 0 {
					t.Fatalf("missing %d rows", wantID)
				}
				back, err := pages.ListHistoryPage(ctx, HistoryPageRequest{SnapshotID: first.SnapshotID})
				if err != nil || !reflect.DeepEqual(first, back) {
					t.Fatalf("back navigation differs: %v", err)
				}
				fresh, err := pages.ListHistoryPage(ctx, HistoryPageRequest{})
				if err != nil || fresh.SnapshotID != int64(count+1) {
					t.Fatalf("refresh: %+v %v", fresh, err)
				}
			})
		})
	}
}

func TestHistoryPageGlobalLiteralFilters(t *testing.T) {
	historyPageStores(t, func(t *testing.T, s Store, pages HistoryPageStore) {
		saveHistoryPageExchange(t, s, Exchange{Method: "GET", Host: "Example.COM", Path: `/Needle%_\`, Query: "token=Unique", InScope: true, ScopeVersion: 7, ResponseIntercepted: true, AppliedRuleIDs: []string{"rule"}})
		for i := 0; i < 201; i++ {
			saveHistoryPageExchange(t, s, Exchange{Method: "POST", Host: "other", Path: "/ordinary", Query: "x=1"})
		}
		yes, no := true, false
		for _, tc := range []struct {
			filter HistoryFilter
			count  int
		}{
			{HistoryFilter{Search: "needle"}, 1}, {HistoryFilter{Search: "%"}, 1}, {HistoryFilter{Search: "_"}, 1},
			{HistoryFilter{Search: `\`}, 1}, {HistoryFilter{Search: "unique"}, 1}, {HistoryFilter{Search: "example.com"}, 1},
			{HistoryFilter{Search: "' OR 1=1 --"}, 0}, {HistoryFilter{Method: "GET", Host: "Example.COM", InScope: &yes}, 1},
			{HistoryFilter{Method: "get"}, 0}, {HistoryFilter{Host: "example.com"}, 0},
			{HistoryFilter{InScope: &yes}, 1}, {HistoryFilter{InScope: &no}, 100},
		} {
			page, err := pages.ListHistoryPage(context.Background(), HistoryPageRequest{Filter: tc.filter})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != tc.count || page.SnapshotID != 202 {
				t.Fatalf("filter %+v: count %d snapshot %d", tc.filter, len(page.Items), page.SnapshotID)
			}
			if tc.count == 1 && (page.Items[0].ID != 1 || page.Items[0].ScopeVersion != 7 || !page.Items[0].ResponseIntercepted || !reflect.DeepEqual(page.Items[0].AppliedRuleIDs, []string{"rule"})) {
				t.Fatalf("metadata: %+v", page.Items[0])
			}
		}
		saveHistoryPageExchange(t, s, Exchange{Path: "/Ä"})
		page, err := pages.ListHistoryPage(context.Background(), HistoryPageRequest{Filter: HistoryFilter{Search: "ä"}})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("non-ASCII must remain case-sensitive: %+v %v", page, err)
		}
	})
}

func TestHistoryPageInvalidCursors(t *testing.T) {
	historyPageStores(t, func(t *testing.T, s Store, pages HistoryPageStore) {
		for _, request := range []HistoryPageRequest{{BeforeID: -1}, {SnapshotID: -1}, {BeforeID: 1}, {BeforeID: 3, SnapshotID: 2}} {
			if _, err := pages.ListHistoryPage(context.Background(), request); err == nil {
				t.Fatalf("accepted %+v", request)
			}
		}
	})
}

func TestHistoryPageMemoryLegacyBounded(t *testing.T) {
	s := NewMemoryForTests()
	for i := 0; i < 101; i++ {
		saveHistoryPageExchange(t, s, Exchange{})
	}
	items, err := s.ListHistory(context.Background(), HistoryFilter{})
	if err != nil || len(items) != 100 {
		t.Fatalf("legacy count %d: %v", len(items), err)
	}
}
