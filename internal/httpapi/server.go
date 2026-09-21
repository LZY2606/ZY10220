// Package httpapi 提供查验台页面与操作接口。
// 所有人工修改（边界改派、拟合窗、曲率点选择）单独持久化，不覆盖原始读数。
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"consolidation-console/internal/consolidation"
	"consolidation-console/internal/domain"
	"consolidation-console/internal/fixture"
	"consolidation-console/internal/store"
	svg "consolidation-console/internal/svg"
)

type Server struct {
	st   *store.Store
	tmpl *template.Template
}

func New(st *store.Store) (*Server, error) {
	t := template.Must(template.New("page").Funcs(template.FuncMap{
		"chartTime": chartTime, "chartE": chartE, "chartPath": chartPath,
	}).Parse(pageHTML))
	return &Server{st: st, tmpl: t}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/ensure-fixture", s.handleEnsureFixture)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/override", s.handleOverride)
	mux.HandleFunc("/api/choose-curve", s.handleChooseCurve)
	mux.HandleFunc("/api/reset", s.handleReset)
	mux.HandleFunc("/api/export", s.handleExport)
	mux.HandleFunc("/api/import", s.handleImport)
	return mux
}

func (s *Server) ensureRun(ctx context.Context) (int64, error) {
	id, err := s.st.LatestRunID(ctx)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return s.seedFixture(ctx)
	}
	return id, nil
}

func (s *Server) seedFixture(ctx context.Context) (int64, error) {
	run, steps, readings, cfgs := fixture.Build()
	snap := domain.ExportRun{
		SchemaVersion: 1, Run: run, Steps: steps, Readings: readings,
		Configs: cfgs, ChosenCurve: -1,
	}
	return s.st.Import(ctx, snap)
}

type pageData struct {
	Run       domain.Run
	Steps     []domain.StepSpec
	Result    *consolidation.Result
	Configs   map[int]domain.StepConfig
	Overrides map[int64]int
	Chosen    int
	Notice    string
}

func (s *Server) loadData(ctx context.Context) (pageData, error) {
	id, err := s.ensureRun(ctx)
	if err != nil {
		return pageData{}, err
	}
	snap, err := s.st.Export(ctx, id)
	if err != nil {
		return pageData{}, err
	}
	cfg := map[int]domain.StepConfig{}
	for _, c := range snap.Configs {
		cfg[c.Step] = c
	}
	ov := map[int64]int{}
	for _, o := range snap.Overrides {
		ov[o.ReadingID] = o.NewStep
	}
	chosen := snap.ChosenCurve
	res := consolidation.Analyze(snap.Run, snap.Steps, snap.Readings, cfg, ov, chosen)
	return pageData{
		Run: snap.Run, Steps: snap.Steps, Result: &res,
		Configs: cfg, Overrides: ov, Chosen: chosen,
	}, nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := s.loadData(r.Context())
	data.Notice = r.URL.Query().Get("notice")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, data); err != nil {
		log.Printf("template: %v", err)
	}
}

func (s *Server) handleEnsureFixture(w http.ResponseWriter, r *http.Request) {
	if _, err := s.ensureRun(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?notice="+queryEscape("已载入固定 fixture"), http.StatusSeeOther)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	id, err := s.ensureRun(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	step, _ := strconv.Atoi(r.FormValue("step"))
	start, _ := strconv.ParseFloat(r.FormValue("start"), 64)
	end, _ := strconv.ParseFloat(r.FormValue("end"), 64)
	method := r.FormValue("method")
	if method != "sqrt" && method != "log" {
		http.Error(w, "method 必须为 sqrt 或 log", 400)
		return
	}
	if start < 0 || end <= start {
		http.Error(w, "拟合窗需满足 0 <= 起始 < 结束", 400)
		return
	}
	if err := s.st.SetConfig(r.Context(), id, domain.StepConfig{
		Step: step, Method: method, WindowStartMin: start, WindowEndMin: end,
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?notice="+queryEscape(fmt.Sprintf("第 %d 级拟合窗已更新", step)),
		http.StatusSeeOther)
}

func (s *Server) handleOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	id, err := s.ensureRun(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	rid, _ := strconv.ParseInt(r.FormValue("reading_id"), 10, 64)
	newStep, _ := strconv.Atoi(r.FormValue("new_step"))
	if err := s.st.SetOverride(r.Context(), id, domain.Override{ReadingID: rid, NewStep: newStep}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	msg := "已恢复原级"
	if newStep != 0 {
		msg = fmt.Sprintf("读数 #%d 改派至第 %d 级", rid, newStep)
	}
	http.Redirect(w, r, "/?notice="+queryEscape(msg), http.StatusSeeOther)
}

func (s *Server) handleChooseCurve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	id, err := s.ensureRun(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	idx, _ := strconv.Atoi(r.FormValue("index"))
	if err := s.st.SetChosen(r.Context(), id, idx); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?notice="+queryEscape("已更新先期固结压力候选选择"),
		http.StatusSeeOther)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if err := s.st.Wipe(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := s.seedFixture(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?notice="+queryEscape("已清空数据库并重新导入固定 fixture"),
		http.StatusSeeOther)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id, err := s.ensureRun(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	snap, err := s.st.Export(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="consolidation-run-%d.json"`, id))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snap)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var snap domain.ExportRun
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
		http.Error(w, "导入 JSON 解析失败: "+err.Error(), 400)
		return
	}
	if snap.SchemaVersion != 1 || len(snap.Readings) == 0 {
		http.Error(w, "快照缺少 schema_version=1 或读数为空", 400)
		return
	}
	// 重放导入：清空后导入，读数主键保留，保证原读数可反查。
	if err := s.st.Wipe(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := s.st.Import(r.Context(), snap); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?notice="+queryEscape(
		fmt.Sprintf("已重放导入 %d 条读数", len(snap.Readings))), http.StatusSeeOther)
}

func queryEscape(s string) string {
	return template.URLQueryEscaper(s)
}

// 供模板调用的绘图函数。
func chartTime(d pageData) template.HTML {
	return template.HTML(svg.TimeDisplacement(d.Result, 960, 300))
}
func chartE(d pageData) template.HTML    { return template.HTML(svg.ELogP(d.Result, 960, 420)) }
func chartPath(d pageData) template.HTML { return template.HTML(svg.LoadPath(d.Result, 960, 260)) }
