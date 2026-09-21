package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"consolidation/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SeedIfEmpty(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(st))
	t.Cleanup(func() { ts.Close(); st.Close() })
	return ts, st
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s -> %d", url, resp.StatusCode)
	}
	return string(body)
}

func postForm(t *testing.T, url string, form url.Values) {
	t.Helper()
	resp, err := http.PostForm(url, form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST %s -> %d", url, resp.StatusCode)
	}
}

func TestIndexPage(t *testing.T) {
	ts, _ := newTestServer(t)
	body := get(t, ts.URL+"/")
	for _, want := range []string{
		"固结路径查验台",
		"荷载路径",
		"时间-位移",
		"孔隙比 - 对数压力",
		"<svg",
		"gauge-reset",
		"clock-rollback",
		"级次边界修正",
		"运行记录",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}

func TestCandidateFlowShowsPcRange(t *testing.T) {
	ts, _ := newTestServer(t)
	// two curvature candidates near the knee of the fixture curve
	postForm(t, ts.URL+"/candidate/add", url.Values{"logp": {"1.85"}, "e": {"0.28"}, "note": {"knee A"}})
	postForm(t, ts.URL+"/candidate/add", url.Values{"logp": {"2.05"}, "e": {"0.24"}, "note": {"knee B"}})
	body := get(t, ts.URL+"/")
	if !strings.Contains(body, "先期固结压力范围") {
		t.Fatal("expected pc range with multiple candidates")
	}
	if !strings.Contains(body, "knee A") || !strings.Contains(body, "knee B") {
		t.Fatal("candidates not listed")
	}
}

func TestFitWindowFlow(t *testing.T) {
	ts, _ := newTestServer(t)
	postForm(t, ts.URL+"/fit/add", url.Values{
		"stage": {"6"}, "segment": {"1"}, "method": {"sqrt"}, "t0": {"0"}, "t1": {"900"},
	})
	body := get(t, ts.URL+"/")
	if !strings.Contains(body, "cv (m²/年)") {
		t.Fatal("fit table missing")
	}
}

func TestStageCorrectionAndExportImport(t *testing.T) {
	ts, st := newTestServer(t)
	postForm(t, ts.URL+"/stage/update", url.Values{
		"no": {"1"}, "pressure": {"12.5"}, "kind": {"load"},
		"start_seq": {"12"}, "end_seq": {"24"},
	})
	resp, err := http.Get(ts.URL + "/export")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), `"start_seq"`) && !strings.Contains(string(data), `"StartSeq"`) {
		t.Fatal("export missing stages")
	}
	// wipe and re-import through the HTTP endpoint
	postForm(t, ts.URL+"/reset", url.Values{})
	resp, err = http.Post(ts.URL+"/import", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	run, _ := st.FirstRun()
	stages, _ := st.Stages(run.ID)
	if stages[1].StartSeq != 12 || stages[1].EndSeq != 24 {
		t.Fatalf("correction lost after import: %+v", stages[1])
	}
	rs, _ := st.Readings(run.ID)
	if len(rs) == 0 {
		t.Fatal("readings lost after import")
	}
}
