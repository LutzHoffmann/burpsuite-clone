package store

import (
	"context"
	"net/http"
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

func TestSQLiteStoreCapsBodiesAndRawData(t *testing.T) {
	t.Setenv("BC_BODY_LIMIT_BYTES", "4")

	st, err := OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ex := &Exchange{
		Method: "POST",
		Request: RequestData{
			Body: []byte("request-body"),
			Raw:  []byte("request-raw"),
		},
		Response: ResponseData{
			Body: []byte("response-body"),
			Raw:  []byte("response-raw"),
		},
	}
	if err := st.SaveExchange(context.Background(), ex); err != nil {
		t.Fatal(err)
	}

	loaded, err := st.GetExchange(context.Background(), ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.RequestTruncated || !loaded.ResponseTruncated {
		t.Fatalf("truncation flags = request:%t response:%t", loaded.RequestTruncated, loaded.ResponseTruncated)
	}
	if len(loaded.Request.Body) > 4 || len(loaded.Request.Raw) > 4 {
		t.Fatalf("request capture exceeds limit: body=%d raw=%d", len(loaded.Request.Body), len(loaded.Request.Raw))
	}
	if len(loaded.Response.Body) > 4 || len(loaded.Response.Raw) > 4 {
		t.Fatalf("response capture exceeds limit: body=%d raw=%d", len(loaded.Response.Body), len(loaded.Response.Raw))
	}
}

func TestOpenSQLiteRecordsAppliedMigrations(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var count int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("expected at least one applied migration")
	}
}

func TestSQLitePersistsRepeaterSessionAndSendHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "project.sqlite")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	record := &RepeaterSend{
		SessionID: "session-1", Method: "POST", URL: "https://example.test/send",
		RequestHeaders: http.Header{"Content-Type": {"text/plain"}}, RequestBody: "request",
		Status: 202, ResponseHeaders: http.Header{"Content-Type": {"text/plain"}}, ResponseBody: "accepted",
		DurationMS: 15, Size: 8, ContentType: "text/plain", SentAt: time.Unix(1700000000, 0).UTC(),
	}
	if err := st.SaveRepeaterSend(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if record.ID == 0 {
		t.Fatal("expected generated send id")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	sessions, err := reopened.ListRepeaterSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "session-1" || sessions[0].SendCount != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	sends, err := reopened.ListRepeaterSends(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sends) != 1 || sends[0].RequestBody != "request" || sends[0].ResponseBody != "accepted" {
		t.Fatalf("sends = %+v", sends)
	}
}

func TestSQLiteProvidesDefaultProjectAndPersistentSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "project.sqlite")
	st, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.ActiveProject(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if project.ID == 0 || project.Name != "Default Project" {
		t.Fatalf("project = %+v", project)
	}
	if err := st.SetSetting(context.Background(), "intercept.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	value, err := reopened.GetSetting(context.Background(), "intercept.enabled")
	if err != nil {
		t.Fatal(err)
	}
	if value != "true" {
		t.Fatalf("setting = %q", value)
	}
}
