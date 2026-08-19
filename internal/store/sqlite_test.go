package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreSavesAndListsExchange(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "project.sqlite")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ex := &Exchange{
		Method:    "GET",
		Scheme:    "https",
		Host:      "example.test",
		Path:      "/login",
		Query:     "next=/app",
		Status:    200,
		MIMEType:  "text/html",
		StartedAt: time.Unix(1700000000, 0).UTC(),
		Duration:  25 * time.Millisecond,
		Request: RequestData{
			Headers: map[string][]string{"User-Agent": {"test"}},
			Body:    []byte("request-body"),
		},
		Response: ResponseData{
			Headers: map[string][]string{"Content-Type": {"text/html"}},
			Body:    []byte("response-body"),
		},
	}

	if err := st.SaveExchange(context.Background(), ex); err != nil {
		t.Fatal(err)
	}
	if ex.ID == 0 {
		t.Fatal("expected generated id")
	}

	items, err := st.ListHistory(context.Background(), HistoryFilter{Search: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].Host != "example.test" || items[0].Status != 200 {
		t.Fatalf("unexpected item: %+v", items[0])
	}

	loaded, err := st.GetExchange(context.Background(), ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Response.Body) != "response-body" {
		t.Fatalf("response body = %q", loaded.Response.Body)
	}
}
