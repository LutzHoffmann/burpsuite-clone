package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func TestFindingsAPI(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "findings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	e := &store.Exchange{Method: "GET", Scheme: "https", Host: "example.test", Path: "/", Status: 200, MIMEType: "text/html", InScope: true, StartedAt: time.Now()}
	if err := repository.SaveExchange(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{Store: repository, APIAddr: "127.0.0.1:9080"})
	rec := storageRequest(s, "GET", "/api/findings?host=example.test&type=hsts_missing", "")
	var page store.FindingsPage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].LatestExchangeID != e.ID {
		t.Fatalf("response %d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"type=unknown", "offset=-1", "offset=1", "snapshotId=0", "host=a&host=b", "bad=1", "host=%zz"} {
		if got := storageRequest(s, "GET", "/api/findings?"+query, ""); got.Code != 400 {
			t.Errorf("%s -> %d", query, got.Code)
		}
	}
}
