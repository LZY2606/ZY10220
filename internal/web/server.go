// Package web serves the consolidation path inspection console over HTTP
// with server-rendered SVG charts.
package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"

	"consolidation/internal/consol"
	"consolidation/internal/store"
)

// Server wires HTTP handlers to the store.
type Server struct {
	st  *store.Store
	mux *http.ServeMux
}

// New builds the handler graph.
func New(st *store.Store) *Server {
	s := &Server{st: st, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /{$}", s.index)
	s.mux.HandleFunc("POST /stage/update", s.stageUpdate)
	s.mux.HandleFunc("POST /fit/add", s.fitAdd)
	s.mux.HandleFunc("POST /fit/delete", s.fitDelete)
	s.mux.HandleFunc("POST /candidate/add", s.candidateAdd)
	s.mux.HandleFunc("POST /candidate/delete", s.candidateDelete)
	s.mux.HandleFunc("GET /export", s.export)
	s.mux.HandleFunc("POST /import", s.importDump)
	s.mux.HandleFunc("POST /reset", s.reset)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) fail(w http.ResponseWriter, code int, err error) {
	http.Error(w, err.Error(), code)
	log.Printf("error: %v", err)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	v, err := buildView(s.st)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	elogp, ec := elogpSVG(v)
	pm := pageModel{
		View:       v,
		LoadPath:   template.HTML(loadPathSVG(v)),
		TimeDisp:   template.HTML(timeDispSVG(v)),
		ELogP:      template.HTML(elogp),
		Stages:     sortedStagesCopy(v.Stages),
		KindColors: kindColors,
	}
	if ec != nil {
		pm.ELogPScale = &scaleInfo{
			XMin: ec.XMin, XMax: ec.XMax, YMin: ec.YMin, YMax: ec.YMax,
			PadL: ec.PadL, PadR: ec.PadR, PadT: ec.PadT, PadB: ec.PadB,
			W: ec.W, H: ec.H, InvertY: ec.InvertY,
		}
		if b, err := json.Marshal(pm.ELogPScale); err == nil {
			pm.ELogPScaleJSON = template.JS(b)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTpl.Execute(w, pm); err != nil {
		log.Printf("template: %v", err)
	}
}

func (s *Server) stageUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	run, err := s.st.FirstRun()
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	no, _ := strconv.Atoi(r.FormValue("no"))
	pressure, _ := strconv.ParseFloat(r.FormValue("pressure"), 64)
	startSeq, _ := strconv.Atoi(r.FormValue("start_seq"))
	endSeq, _ := strconv.Atoi(r.FormValue("end_seq"))
	st := consol.Stage{
		No: no, PressureKPa: pressure, Kind: consol.StageKind(r.FormValue("kind")),
		StartSeq: startSeq, EndSeq: endSeq,
	}
	if st.EndSeq < st.StartSeq {
		s.fail(w, http.StatusBadRequest, fmt.Errorf("end_seq < start_seq"))
		return
	}
	if err := s.st.UpdateStageBounds(run.ID, st); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) fitAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	run, err := s.st.FirstRun()
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	stageNo, _ := strconv.Atoi(r.FormValue("stage"))
	segIdx, _ := strconv.Atoi(r.FormValue("segment"))
	t0, _ := strconv.ParseFloat(r.FormValue("t0"), 64)
	t1, _ := strconv.ParseFloat(r.FormValue("t1"), 64)
	w2 := consol.FitWindow{Method: r.FormValue("method"), T0S: t0, T1S: t1}
	if t1 <= t0 {
		s.fail(w, http.StatusBadRequest, fmt.Errorf("拟合窗 t1 必须大于 t0"))
		return
	}
	if err := s.st.SaveFit(run.ID, stageNo, segIdx, w2); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) fitDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.st.DeleteFit(id); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) candidateAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	run, err := s.st.FirstRun()
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	logp, _ := strconv.ParseFloat(r.FormValue("logp"), 64)
	e, _ := strconv.ParseFloat(r.FormValue("e"), 64)
	if _, err := s.st.AddCandidate(run.ID, consol.CurvatureCandidate{
		LogP: logp, E: e, Note: r.FormValue("note"),
	}); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) candidateDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.st.DeleteCandidate(id); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	data, err := s.st.ExportJSON()
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="run-record.json"`)
	w.Write(data)
}

func (s *Server) importDump(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	// Accept either a raw JSON body or a multipart file field.
	if ct := r.Header.Get("Content-Type"); len(ct) > 19 && ct[:19] == "multipart/form-data" {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			s.fail(w, http.StatusBadRequest, err)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			s.fail(w, http.StatusBadRequest, err)
			return
		}
		defer f.Close()
		if body, err = io.ReadAll(f); err != nil {
			s.fail(w, http.StatusBadRequest, err)
			return
		}
	}
	if err := s.st.ImportJSON(body); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Clear(); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.st.SeedIfEmpty(); err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
