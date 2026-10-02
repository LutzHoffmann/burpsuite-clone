package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func storageRequest(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:9080"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestHistoryPageAPI(t *testing.T) {
	repository := store.NewMemoryForTests()
	for i := 1; i <= 105; i++ {
		if err := repository.SaveExchange(context.Background(), &store.Exchange{Method: "GET", Path: fmt.Sprintf("/%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewServer(Config{Store: repository, APIAddr: "127.0.0.1:9080"})
	w := storageRequest(s, "GET", "/api/history/page", "")
	var page struct {
		Items        []historyItemDTO `json:"items"`
		NextBeforeID int64            `json:"nextBeforeId"`
		SnapshotID   int64            `json:"snapshotId"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 100 || page.NextBeforeID != 6 || page.SnapshotID != 105 {
		t.Fatalf("page: %d %s", w.Code, w.Body.String())
	}
	w = storageRequest(s, "GET", "/api/history/page?beforeId=6&snapshotId=105", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 5 || page.NextBeforeID != 0 {
		t.Fatalf("last page: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"beforeId=-1", "snapshotId=0", "beforeId=1", "beforeId=8&snapshotId=5", "snapshotId=no", "inScope=maybe", "beforeId=1&beforeId=2&snapshotId=5", "inScope=%zz", "snapshotId=%zz", "search=%zz"} {
		if got := storageRequest(s, "GET", "/api/history/page?"+query, ""); got.Code != 400 {
			t.Errorf("query %s: status %d", query, got.Code)
		}
	}
	w = storageRequest(s, "GET", "/api/history", "")
	var legacy []historyItemDTO
	if json.Unmarshal(w.Body.Bytes(), &legacy) != nil || len(legacy) != 100 {
		t.Fatalf("legacy not bounded: %s", w.Body.String())
	}
}

func TestStorageAPIAndRepeaterQuota(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "quota.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("response survives")) }))
	defer target.Close()
	s := NewServer(Config{Store: repository, Repeater: repeater.NewService(nil, 1024), APIAddr: "127.0.0.1:9080"})
	w := storageRequest(s, "GET", "/api/storage", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"limitBytes":1073741824`) {
		t.Fatalf("storage: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"limitBytes":0}`, `{"limitBytes":-1}`, `{"limitBytes":1099511627777}`, `{"limitBytes":1.5}`} {
		if got := storageRequest(s, "PUT", "/api/storage", body); got.Code != 400 {
			t.Errorf("invalid limit %s: %d", body, got.Code)
		}
	}
	if got := storageRequest(s, "PUT", "/api/storage", `{"limitBytes":1}`); got.Code != 200 {
		t.Fatalf("update: %d %s", got.Code, got.Body.String())
	}
	w = storageRequest(s, "POST", "/api/repeater/sessions/test/send", fmt.Sprintf(`{"method":"GET","url":%q}`, target.URL))
	var result struct {
		Body    string `json:"body"`
		Saved   *bool  `json:"saved"`
		Warning string `json:"storageWarning"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Body != "response survives" || result.Saved == nil || *result.Saved || result.Warning == "" {
		t.Fatalf("repeater: %d %s", w.Code, w.Body.String())
	}
	w = storageRequest(s, "GET", "/api/storage", "")
	if !strings.Contains(w.Body.String(), `"paused":true`) {
		t.Fatalf("not paused: %s", w.Body.String())
	}
	sends, err := repository.ListRepeaterSends(context.Background(), "test")
	if err != nil || len(sends) != 0 {
		t.Fatalf("skipped send persisted: %v %v", sends, err)
	}
}
