package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"consolidation-console/internal/store"
)

func testServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Routes())
	ts.Client().CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return ts, func() { ts.Close(); st.Close() }
}

func TestIndexSeedsFixtureAndTitle(t *testing.T) {
	ts, cleanup := testServer(t)
	defer cleanup()
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "固结路径查验台") {
		t.Fatal("title missing")
	}
	if strings.Count(string(body), "<svg") != 3 {
		t.Fatalf("expected 3 svg charts, got %d", strings.Count(string(body), "<svg"))
	}
	if !strings.Contains(string(body), "时钟回退") {
		t.Fatal("rollback diagnostic missing from page")
	}
	if !strings.Contains(string(body), "pc≈124") {
		t.Fatal("Casagrande pc annotation missing")
	}
}

func TestConfigOverrideChooseEndpoints(t *testing.T) {
	ts, cleanup := testServer(t)
	defer cleanup()

	post := func(path string, vals url.Values) *http.Response {
		t.Helper()
		resp, err := ts.Client().PostForm(ts.URL+path, vals)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := post("/api/config", url.Values{
		"step": {"1"}, "method": {"log"}, "start": {"1"}, "end": {"25"},
	}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("config status=%d", r.StatusCode)
	}
	if r := post("/api/override", url.Values{
		"reading_id": {"37"}, "new_step": {"4"},
	}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("override status=%d", r.StatusCode)
	}
	if r := post("/api/choose-curve", url.Values{"index": {"1"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("choose status=%d", r.StatusCode)
	}
	resp, err := ts.Client().Get(ts.URL + "/api/export")
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if snap["chosen_curve"].(float64) != 1 {
		t.Fatalf("chosen not persisted: %v", snap["chosen_curve"])
	}
}

func TestExportImportReplay(t *testing.T) {
	ts, cleanup := testServer(t)
	defer cleanup()
	resp, err := ts.Client().Get(ts.URL + "/api/export")
	if err != nil {
		t.Fatal(err)
	}
	var snap bytes.Buffer
	io.Copy(&snap, resp.Body)
	resp.Body.Close()

	// 修改后清空重放原始快照。
	ts.Client().PostForm(ts.URL+"/api/override", url.Values{"reading_id": {"37"}, "new_step": {"4"}})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/import", bytes.NewReader(snap.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	r2, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusSeeOther {
		t.Fatalf("import status=%d", r2.StatusCode)
	}
	resp2, _ := http.Get(ts.URL + "/api/export")
	var out map[string]any
	json.NewDecoder(resp2.Body).Decode(&out)
	resp2.Body.Close()
	if ov, ok := out["overrides"].([]any); ok && len(ov) != 0 {
		t.Fatal("replay must reset overrides")
	}
}

func TestRejectInvalidConfig(t *testing.T) {
	ts, cleanup := testServer(t)
	defer cleanup()
	r, err := ts.Client().PostForm(ts.URL+"/api/config", url.Values{
		"step": {"1"}, "method": {"bogus"}, "start": {"1"}, "end": {"2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", r.StatusCode)
	}
}

func TestResetReseeds(t *testing.T) {
	ts, cleanup := testServer(t)
	defer cleanup()
	r, err := ts.Client().PostForm(ts.URL+"/api/reset", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset status=%d", r.StatusCode)
	}
	resp, err := ts.Client().Get(ts.URL + "/api/export")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"id": 169`) {
		t.Fatal("expected full fixture after reset")
	}
}
