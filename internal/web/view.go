package web

import (
	"fmt"
	"math"
	"sort"

	"consolidation/internal/consol"
	"consolidation/internal/store"
)

// SegmentView bundles one diagnosed segment with its default fit outcomes.
type SegmentView struct {
	StageNo    int
	Index      int
	Flags      []string
	N          int
	T0S, T1S   float64
	DrainMM    float64
	SqrtResult *consol.FitResult
	SqrtErr    string
	LogResult  *consol.FitResult
	LogErr     string
}

// FitView pairs a saved (manual) fit window with its computed result.
type FitView struct {
	Row    store.FitRow
	Result *consol.FitResult
	Err    string
}

// CandidateView aligns each saved curvature candidate with its own
// Casagrande outcome (or the reason the construction failed).
type CandidateView struct {
	Row store.CandidateRow
	Pc  *float64
	Err string
}

// ViewData is everything the page needs, grouped so raw readings, corrected
// values, plot geometry and manual selections stay visibly separate.
type ViewData struct {
	Run        store.RunRow
	Stages     []consol.Stage
	Points     []consol.CorrectedPoint
	Segments   []SegmentView
	Path       []consol.PathPoint
	ELogP      []consol.ELogPPoint
	Skipped    []consol.PathPoint // non-positive-pressure states (kept off the log axis)
	Branch     []consol.ELogPPoint
	SavedFits  []FitView
	Candidates []store.CandidateRow
	CandViews  []CandidateView
	PcResults  []consol.CasagrandeResult
	PcLo, PcHi float64
	HasPc      bool
	PcErrs     []string

	// cumulative time (seconds) per reading, monotonic within a segment
	CumTimeS []float64
}

// stageSegments splits readings into segments using the current (possibly
// user-corrected) stage boundaries.
func stageSegments(stages []consol.Stage, readings []consol.Reading) map[int][]consol.Segment {
	out := map[int][]consol.Segment{}
	for _, st := range stages {
		var rs []consol.Reading
		for _, r := range readings {
			if st.Contains(r.Seq) {
				rs = append(rs, r)
			}
		}
		out[st.No] = consol.SplitSegments(st.No, rs)
	}
	return out
}

// buildView assembles the full view model from the store.
func buildView(st *store.Store) (*ViewData, error) {
	run, err := st.FirstRun()
	if err != nil {
		return nil, err
	}
	readings, err := st.Readings(run.ID)
	if err != nil {
		return nil, err
	}
	stages, err := st.Stages(run.ID)
	if err != nil {
		return nil, err
	}
	v := &ViewData{Run: run, Stages: stages}
	v.Points = consol.Correct(readings, run.Specimen)

	// Segments + cumulative time (never stitched across anomalies).
	segsByStage := stageSegments(stages, readings)
	v.CumTimeS = make([]float64, len(readings))
	var clock float64
	for _, st := range stages {
		for _, seg := range segsByStage[st.No] {
			sv := SegmentView{StageNo: st.No, Index: seg.Index, Flags: seg.Flags, N: len(seg.Readings)}
			if len(seg.Readings) > 0 {
				sv.T0S = seg.Readings[0].ElapsedS
				sv.T1S = seg.Readings[len(seg.Readings)-1].ElapsedS
			}
			base := clock
			t0 := seg.Readings[0].ElapsedS
			var hSum float64
			for _, r := range seg.Readings {
				v.CumTimeS[r.Seq] = base + (r.ElapsedS - t0)
				v.Points[r.Seq].SegmentIdx = seg.Index
				hSum += v.Points[r.Seq].HeightMM
				clock = math.Max(clock, base+(r.ElapsedS-t0))
			}
			drain := hSum / float64(len(seg.Readings))
			if run.Specimen.DoubleDrain {
				drain /= 2
			}
			sv.DrainMM = drain
			// Default diagnostic windows: sqrt over the early straight
			// portion, log over the full segment.
			tEnd := math.Max(sv.T1S-sv.T0S, 1)
			if r, err := consol.FitSegment(seg, consol.FitWindow{Method: "sqrt", T0S: sv.T0S, T1S: sv.T0S + tEnd*0.5}, drain); err == nil {
				sv.SqrtResult = &r
			} else {
				sv.SqrtErr = err.Error()
			}
			if r, err := consol.FitSegment(seg, consol.FitWindow{Method: "log", T0S: math.Max(sv.T0S, 1), T1S: sv.T1S}, drain); err == nil {
				sv.LogResult = &r
			} else {
				sv.LogErr = err.Error()
			}
			v.Segments = append(v.Segments, sv)
		}
		clock += 1 // keep stage starts strictly increasing
	}

	// True load path and e-log(p) geometry.
	v.Path = consol.LoadPath(stages, v.Points)
	v.ELogP, v.Skipped = consol.ELogP(v.Path)
	v.Branch = consol.FirstLoadingBranch(v.ELogP)

	// Manual fit windows with computed results.
	fits, err := st.Fits(run.ID)
	if err != nil {
		return nil, err
	}
	for _, f := range fits {
		fv := FitView{Row: f}
		var seg *consol.Segment
		for _, s := range segsByStage[f.StageNo] {
			if s.Index == f.SegmentIdx {
				cp := s
				seg = &cp
			}
		}
		if seg == nil {
			fv.Err = "级次/分段不存在（边界可能已修改）"
		} else if r, err := consol.FitSegment(*seg, f.Window, drainFor(v, f.StageNo, f.SegmentIdx, segsByStage)); err != nil {
			fv.Err = err.Error()
		} else {
			fv.Result = &r
		}
		v.SavedFits = append(v.SavedFits, fv)
	}

	// Curvature candidates and the Casagrande construction.
	v.Candidates, err = st.Candidates(run.ID)
	if err != nil {
		return nil, err
	}
	var cands []consol.CurvatureCandidate
	for _, c := range v.Candidates {
		c.Pt.ID = int(c.ID)
		cands = append(cands, c.Pt)
	}
	res, lo, hi, errs := consol.PcRange(cands, v.Branch)
	v.PcResults = res
	for _, e := range errs {
		v.PcErrs = append(v.PcErrs, e.Error())
	}
	pcByCand := map[int]float64{}
	for _, r := range res {
		pcByCand[r.Candidate.ID] = r.PcKPa
	}
	for _, c := range v.Candidates {
		cv := CandidateView{Row: c}
		if pc, ok := pcByCand[int(c.ID)]; ok {
			cv.Pc = &pc
		} else if _, err := consol.Casagrande(c.Pt, v.Branch); err != nil {
			cv.Err = err.Error()
		}
		v.CandViews = append(v.CandViews, cv)
	}
	if len(res) > 0 {
		v.HasPc = true
		v.PcLo, v.PcHi = lo, hi
	}
	return v, nil
}

func drainFor(v *ViewData, stageNo, segIdx int, _ map[int][]consol.Segment) float64 {
	for _, sv := range v.Segments {
		if sv.StageNo == stageNo && sv.Index == segIdx {
			return sv.DrainMM
		}
	}
	return 0
}

// --- chart builders ---

var kindColors = map[consol.StageKind]string{
	consol.KindInitial: "#888888",
	consol.KindLoad:    "#1f6fb2",
	consol.KindUnload:  "#c0392b",
	consol.KindReload:  "#1e8449",
}

// loadPathSVG renders the true load path (record order, never re-sorted).
func loadPathSVG(v *ViewData) string {
	if len(v.Path) == 0 {
		return ""
	}
	xmax := float64(v.Path[len(v.Path)-1].Seq)
	var ymax float64
	for _, p := range v.Path {
		ymax = math.Max(ymax, p.PressureKPa)
	}
	c := newChart(860, 260, 0, xmax*1.02+1, 0, ymax*1.1+1)
	// Step plot in true record order.
	var line []pt
	for i, p := range v.Path {
		x := float64(p.Seq)
		if i > 0 {
			line = append(line, pt{x, v.Path[i-1].PressureKPa})
		}
		line = append(line, pt{x, p.PressureKPa})
	}
	c.polyline(line, "#333", 1.6, "")
	for _, p := range v.Path {
		col := kindColors[p.Kind]
		c.circle(pt{float64(p.Seq), p.PressureKPa}, 4, col, "#fff")
		c.text(c.px(float64(p.Seq)), c.py(p.PressureKPa)-9, "middle", "10", col,
			fmt.Sprintf("%g", p.PressureKPa))
	}
	c.axes("记录序号 seq（真实加载顺序）", "压力 p (kPa)",
		ticks(0, xmax, 9), ticks(0, ymax, 6), fmt0, fmt0)
	return c.String()
}

// timeDispSVG renders cumulative time vs cumulative displacement, one
// polyline per diagnosed segment so resets/rollbacks are never stitched.
func timeDispSVG(v *ViewData) string {
	if len(v.Points) == 0 {
		return ""
	}
	var xmax, ymax float64
	for i, p := range v.Points {
		xmax = math.Max(xmax, v.CumTimeS[i])
		ymax = math.Max(ymax, p.CumDialMM)
	}
	c := newChart(860, 300, 0, xmax*1.03+1, 0, ymax*1.08+0.01)
	segColors := []string{"#1f6fb2", "#c0392b", "#1e8449", "#8e44ad", "#b9770e"}
	// group consecutive points by (stage, segment)
	type key struct{ st, sg int }
	groups := map[key][]pt{}
	var order []key
	for i, p := range v.Points {
		k := key{p.Raw.Stage, p.SegmentIdx}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], pt{v.CumTimeS[i] / 60, p.CumDialMM})
	}
	for i, k := range order {
		pts := groups[k]
		col := segColors[i%len(segColors)]
		c.polyline(pts, col, 1.6, "")
		for _, p := range pts {
			c.circle(p, 2.2, col, "none")
		}
	}
	// stage boundary markers
	for _, st := range v.Stages {
		if st.StartSeq < len(v.CumTimeS) && st.No > 0 {
			c.vline(v.CumTimeS[st.StartSeq]/60, "#bbb", "3,3")
		}
	}
	c.axes("累计时间 (min，分段内单调)", "累计位移 (mm)",
		ticks(0, xmax/60, 9), ticks(0, ymax, 6), fmt0, fmt2)
	return c.String()
}

// elogpSVG renders the e-log(p) diagram: full path in record order (the
// hysteresis loop), the first-loading branch, virgin line, bisectors and
// the pc range band. Non-positive pressures never reach the log axis.
func elogpSVG(v *ViewData) (string, *chart) {
	if len(v.ELogP) == 0 {
		return "", nil
	}
	xmin, xmax := math.Inf(1), math.Inf(-1)
	ymin, ymax := math.Inf(1), math.Inf(-1)
	for _, p := range v.ELogP {
		xmin = math.Min(xmin, p.LogP)
		xmax = math.Max(xmax, p.LogP)
		ymin = math.Min(ymin, p.VoidRatio)
		ymax = math.Max(ymax, p.VoidRatio)
	}
	xpad := (xmax - xmin) * 0.06
	ypad := (ymax - ymin) * 0.08
	c := newChart(860, 380, xmin-xpad, xmax+xpad, ymin-ypad, ymax+ypad)
	c.InvertY = true // e grows upward on screen? geotech plots put smaller e lower

	// pc range band behind everything
	if v.HasPc {
		lo, hi := math.Log10(v.PcLo), math.Log10(v.PcHi)
		fmt.Fprintf(&c.body,
			`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="#f5b041" fill-opacity="0.25"/>`,
			c.px(lo), c.PadT, c.px(hi)-c.px(lo), c.H-c.PadT-c.PadB)
	}

	// full path in record order: unload/reload branches stay visible and
	// are never merged into the first-loading curve.
	var pathPts []pt
	for _, p := range v.ELogP {
		pathPts = append(pathPts, pt{p.LogP, p.VoidRatio})
	}
	c.polyline(pathPts, "#999", 1.2, "")
	for _, p := range v.ELogP {
		c.circle(pt{p.LogP, p.VoidRatio}, 3.2, kindColors[p.Kind], "#fff")
	}
	// first-loading branch emphasised
	var br []pt
	for _, p := range v.Branch {
		br = append(br, pt{p.LogP, p.VoidRatio})
	}
	c.polyline(br, "#1f6fb2", 2.2, "")

	// Casagrande geometry per candidate
	for _, r := range v.PcResults {
		x0, x1 := c.XMin, c.XMax
		c.polyline([]pt{{x0, r.VirginLine.At(x0)}, {x1, r.VirginLine.At(x1)}}, "#1f6fb2", 1.2, "6,3")
		c.polyline([]pt{{x0, r.Bisector.At(x0)}, {x1, r.Bisector.At(x1)}}, "#c0392b", 1.2, "6,3")
		c.circle(pt{r.Candidate.LogP, r.Candidate.E}, 5, "#c0392b", "#fff")
		pc := math.Log10(r.PcKPa)
		c.vline(pc, "#c0392b", "4,3")
		c.text(c.px(pc), c.PadT+12, "middle", "10", "#c0392b",
			fmt.Sprintf("pc=%.1f", r.PcKPa))
	}
	for _, cd := range v.Candidates {
		c.circle(pt{cd.Pt.LogP, cd.Pt.E}, 5, "none", "#c0392b")
	}

	c.axes("log10 p (kPa)", "孔隙比 e",
		ticks(c.XMin, c.XMax, 7), ticks(c.YMin, c.YMax, 6), fmt2, fmt2)
	return c.String(), c
}

// sortedStagesCopy returns stages sorted by number (defensive copy for the
// template).
func sortedStagesCopy(st []consol.Stage) []consol.Stage {
	out := append([]consol.Stage(nil), st...)
	sort.Slice(out, func(i, j int) bool { return out[i].No < out[j].No })
	return out
}
