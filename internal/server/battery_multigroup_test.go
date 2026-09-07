package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/tests"
)

func postBatteryRun(t *testing.T, srv *Server, payload map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/runner/battery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func seedTwoGroups(t *testing.T, srv *Server) (t1, t2 tests.Test) {
	t.Helper()
	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "g1", Name: "G One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.testsStore.CreateGroup(tests.Group{ID: "g2", Name: "G Two"}); err != nil {
		t.Fatal(err)
	}
	var err error
	t1, err = srv.testsStore.CreateTest(tests.Test{Name: "T1", Prompt: "say hi", GroupID: "g1", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	t2, err = srv.testsStore.CreateTest(tests.Test{Name: "T2", Prompt: "say ho", GroupID: "g2", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return t1, t2
}

func fakeOllamaForBattery() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		default:
			// Anything else (e.g. /api/chat) fails fast so the async run
			// completes with error results instead of hanging.
			http.Error(w, "nope", http.StatusInternalServerError)
		}
	}))
}

func waitForRun(t *testing.T, srv *Server, runID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/runner/runs/"+runID, nil)
		rr := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			var run map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			// The completion callback keeps writing (model TPS records)
			// right after the run becomes visible; let it settle so the
			// TempDir cleanup does not race open files (Windows).
			time.Sleep(1500 * time.Millisecond)
			return run
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("run %s never completed", runID)
	return nil
}

func runResultTestIDs(run map[string]any) map[string]bool {
	ids := map[string]bool{}
	if res, ok := run["results"].([]any); ok {
		for _, r := range res {
			if m, ok := r.(map[string]any); ok {
				if id, ok := m["test_id"].(string); ok {
					ids[id] = true
				}
			}
		}
	}
	return ids
}

func TestBatteryRunMultipleGroups(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	t1, t2 := seedTwoGroups(t, srv)

	code, out := postBatteryRun(t, srv, map[string]any{
		"group_ids": []string{"g1", "g2"},
		"model_ids": []string{"m"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	runID, _ := out["run_id"].(string)
	if runID == "" {
		t.Fatalf("no run_id in %v", out)
	}
	run := waitForRun(t, srv, runID)
	ids := runResultTestIDs(run)
	if !ids[t1.ID] || !ids[t2.ID] {
		t.Fatalf("run covered %v, want both %q and %q", ids, t1.ID, t2.ID)
	}
	if name, _ := run["group_name"].(string); name != "G One, G Two" {
		t.Fatalf("group_name = %q, want %q", name, "G One, G Two")
	}
}

func TestBatteryRunUnknownGroupID(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)

	code, _ := postBatteryRun(t, srv, map[string]any{
		"group_ids": []string{"nope"},
		"model_ids": []string{"m"},
	})
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestBatteryRunSingleGroupLegacy(t *testing.T) {
	ollamaSrv := fakeOllamaForBattery()
	defer ollamaSrv.Close()
	srv := newTestServer(t, ollamaSrv.URL)
	t1, _ := seedTwoGroups(t, srv)

	code, out := postBatteryRun(t, srv, map[string]any{
		"group_id":  "g1",
		"model_ids": []string{"m"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	run := waitForRun(t, srv, out["run_id"].(string))
	ids := runResultTestIDs(run)
	if !ids[t1.ID] || len(ids) != 1 {
		t.Fatalf("run covered %v, want only %q", ids, t1.ID)
	}
}
