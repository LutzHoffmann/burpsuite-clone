package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/api"
	"github.com/lutzifer/burpsuite-clone/internal/intruder"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

type intruderScopeFunc func(string) bool

func (f intruderScopeFunc) Allows(url string) bool { return f(url) }

func TestIntruderAuthorizedHTTPThroughAPI(t *testing.T) {
	var sends atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.URL.Query().Get("q")))
	}))
	defer target.Close()
	project, err := store.OpenSQLite(filepath.Join(t.TempDir(), "project.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	scope := intruderScopeFunc(func(url string) bool { return strings.HasPrefix(url, target.URL+"/") })
	service, err := intruder.NewService(context.Background(), project, scope, repeater.NewHTTPSender(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	apiServer := httptest.NewServer(api.NewServer(api.Config{Intruder: service, APIAddr: "127.0.0.1:9080"}).Handler())
	defer apiServer.Close()
	client := apiServer.Client()
	raw := fmt.Sprintf("GET /?q=x HTTP/1.1\r\nHost: %s\r\n\r\n", strings.TrimPrefix(target.URL, "http://"))
	body := map[string]interface{}{"config": map[string]interface{}{
		"attack": "sniper", "template": map[string]interface{}{"method": "GET", "url": target.URL + "/?q=x", "raw": base64.StdEncoding.EncodeToString([]byte(raw))},
		"positions":    []map[string]interface{}{{"ID": "p1", "Start": 8, "End": 9, "PayloadSetID": "s1"}},
		"payloadSets":  []map[string]interface{}{{"ID": "s1", "Payloads": []string{base64.StdEncoding.EncodeToString([]byte("a")), base64.StdEncoding.EncodeToString([]byte("b"))}}},
		"requestLimit": 2, "concurrency": 1, "ratePerSecond": 100, "timeoutMs": 1000,
	}}
	encoded, _ := json.Marshal(body)
	request, _ := http.NewRequest(http.MethodPost, apiServer.URL+"/api/intruder/jobs", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	var job struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	request, _ = http.NewRequest(http.MethodPost, apiServer.URL+"/api/intruder/jobs/"+job.ID+"/start", strings.NewReader(fmt.Sprintf(`{"revision":%d}`, job.Revision)))
	request.Header.Set("Content-Type", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	deadline := time.After(2 * time.Second)
	for {
		current, err := service.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == intruder.StateCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("job did not complete: %+v", current)
		case <-time.After(time.Millisecond):
		}
	}
	if sends.Load() != 2 {
		t.Fatalf("sends=%d", sends.Load())
	}
	page, err := service.ListResults(context.Background(), job.ID, intruder.ResultQuery{Limit: 10})
	if err != nil || len(page.Results) != 2 || page.Results[0].Sequence != 1 || page.Results[1].Sequence != 0 {
		t.Fatalf("results=%+v err=%v", page, err)
	}
}
