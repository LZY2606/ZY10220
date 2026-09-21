// Package consol implements the domain logic of the consolidation path
// inspection console: load-path reconstruction, stage segmentation on
// instrument anomalies, displacement/void-ratio corrections, sqrt-time and
// log-time fitting, and the Casagrande preconsolidation construction.
//
// The package is pure: it never touches the database or the network.
package consol

import (
	"fmt"
	"math"
	"sort"
)

// Reading is one raw observation, exactly as recorded by the instrument.
// Raw readings are never mutated; all corrections are derived values.
type Reading struct {
	Seq         int     // monotonic record order (the true load path order)
	Stage       int     // stage number as recorded by the operator
	PressureKPa float64 // applied total stress for this reading
	ElapsedS    float64 // stage-relative clock as recorded
	DialMM      float64 // raw displacement gauge reading (compression positive)
	Note        string  // operator note, e.g. "RESET" for a gauge reset
}

// StageKind classifies a load stage within the true path.
type StageKind string

const (
	KindInitial StageKind = "initial" // zero-load seating state
	KindLoad    StageKind = "load"    // first-time loading
	KindUnload  StageKind = "unload"
	KindReload  StageKind = "reload"
)

// Stage is a load stage with (possibly user-corrected) boundaries over the
// raw reading sequence. Boundaries are inclusive sequence numbers.
type Stage struct {
	No          int
	PressureKPa float64
	Kind        StageKind
	StartSeq    int
	EndSeq      int
}

// Contains reports whether seq falls inside the stage boundaries.
func (s Stage) Contains(seq int) bool { return seq >= s.StartSeq && seq <= s.EndSeq }

// Specimen holds the specimen constants used for corrections.
type Specimen struct {
	Height0MM   float64 // initial specimen height
	VoidRatio0  float64 // initial void ratio e0
	DoubleDrain bool    // true: drainage path = H/2
}

// ResetDropMM is the dial drop magnitude treated as a gauge reset when the
// operator did not flag it explicitly.
const ResetDropMM = 1.0

// Segment is a contiguous run of readings inside one stage with no clock
// rollback and no gauge reset. Segments are diagnosed separately and never
// stitched into a fake continuous trend.
type Segment struct {
	StageNo  int
	Index    int // 0-based within the stage
	Readings []Reading
	Flags    []string // diagnostics, e.g. "clock-rollback", "gauge-reset"
}

// SplitSegments splits the readings of one stage (already filtered and in
// seq order) at clock rollbacks and gauge resets.
func SplitSegments(stageNo int, rs []Reading) []Segment {
	var segs []Segment
	cur := Segment{StageNo: stageNo}
	for i, r := range rs {
		if i > 0 {
			prev := rs[i-1]
			switch {
			case r.ElapsedS < prev.ElapsedS:
				segs = append(segs, cur)
				cur = Segment{StageNo: stageNo, Index: cur.Index + 1,
					Flags: []string{"clock-rollback"}}
			case isReset(prev, r):
				segs = append(segs, cur)
				cur = Segment{StageNo: stageNo, Index: cur.Index + 1,
					Flags: []string{"gauge-reset"}}
			}
		}
		cur.Readings = append(cur.Readings, r)
	}
	if len(cur.Readings) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

func isReset(prev, cur Reading) bool {
	if cur.Note == "RESET" {
		return true
	}
	return prev.DialMM-cur.DialMM > ResetDropMM
}

// CorrectedPoint pairs a raw reading with its derived values. Raw and
// corrected data stay side by side so every correction can be traced back.
type CorrectedPoint struct {
	Raw        Reading
	CumDialMM  float64 // cumulative displacement, gauge resets compensated
	HeightMM   float64 // specimen height after compression
	VoidRatio  float64 // void ratio from thickness correction
	SegmentIdx int     // segment within the stage (-1 when unassigned)
}

// Correct walks readings in true record order, compensates gauge resets by
// carrying an offset, and derives height / void ratio. Clock rollbacks do
// not affect displacement but are reported through segmentation.
func Correct(rs []Reading, spec Specimen) []CorrectedPoint {
	out := make([]CorrectedPoint, 0, len(rs))
	offset := 0.0
	var base float64 // cumulative dial at the very first reading
	for i, r := range rs {
		if i > 0 && isReset(rs[i-1], r) {
			offset = out[i-1].CumDialMM - r.DialMM
		}
		cum := r.DialMM + offset
		if i == 0 {
			base = cum
		}
		deltaH := cum - base
		h := spec.Height0MM - deltaH
		e := spec.VoidRatio0 - (1+spec.VoidRatio0)*deltaH/spec.Height0MM
		out = append(out, CorrectedPoint{
			Raw: r, CumDialMM: cum, HeightMM: h, VoidRatio: e, SegmentIdx: -1,
		})
	}
	return out
}

// PathPoint is one vertex of the true load path, in record order. The path
// is never re-sorted by pressure, so unload/reload branches stay distinct.
type PathPoint struct {
	Seq         int
	StageNo     int
	Kind        StageKind
	PressureKPa float64
	CumDialMM   float64
	VoidRatio   float64
}

// LoadPath returns one path vertex per stage boundary crossing, in record
// order. stages must be sorted by StartSeq.
func LoadPath(stages []Stage, pts []CorrectedPoint) []PathPoint {
	var path []PathPoint
	for _, st := range stages {
		var last *CorrectedPoint
		for i := range pts {
			if st.Contains(pts[i].Raw.Seq) {
				last = &pts[i]
			}
		}
		if last == nil {
			continue
		}
		path = append(path, PathPoint{
			Seq: last.Raw.Seq, StageNo: st.No, Kind: st.Kind,
			PressureKPa: st.PressureKPa, CumDialMM: last.CumDialMM,
			VoidRatio: last.VoidRatio,
		})
	}
	return path
}

// ELogPPoint is a plottable point of the e-log(p) diagram. Points with
// non-positive pressure are excluded from the log axis but reported through
// Skipped so the zero-load initial state is preserved elsewhere.
type ELogPPoint struct {
	PathPoint
	LogP float64
}

// ELogP splits the load path into plottable points (p > 0, path order
// preserved) and skipped non-positive-pressure states.
func ELogP(path []PathPoint) (pts []ELogPPoint, skipped []PathPoint) {
	for _, p := range path {
		if p.PressureKPa <= 0 {
			skipped = append(skipped, p)
			continue
		}
		pts = append(pts, ELogPPoint{PathPoint: p, LogP: math.Log10(p.PressureKPa)})
	}
	return pts, skipped
}

// FirstLoadingBranch filters e-log(p) points belonging to first-time
// loading, sorted by pressure. Unload/reload points never enter this branch,
// so the virgin compression line is not contaminated.
func FirstLoadingBranch(pts []ELogPPoint) []ELogPPoint {
	var out []ELogPPoint
	for _, p := range pts {
		if p.Kind == KindLoad {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PressureKPa < out[j].PressureKPa
	})
	return out
}

// --- regression helpers ---

// Line is y = A + B*x.
type Line struct{ A, B float64 }

// At evaluates the line.
func (l Line) At(x float64) float64 { return l.A + l.B*x }

// FitLine is an ordinary least-squares line fit.
func FitLine(xs, ys []float64) (Line, error) {
	n := len(xs)
	if n < 2 || n != len(ys) {
		return Line{}, fmt.Errorf("need >=2 paired samples, got %d/%d", n, len(ys))
	}
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	den := float64(n)*sxx - sx*sx
	if math.Abs(den) < 1e-12 {
		return Line{}, fmt.Errorf("degenerate x samples")
	}
	b := (float64(n)*sxy - sx*sy) / den
	a := (sy - b*sx) / float64(n)
	return Line{A: a, B: b}, nil
}

// FitWindow selects the time window [T0S, T1S] of a fitting method.
type FitWindow struct {
	Method string  // "sqrt" or "log"
	T0S    float64 // window start, seconds (exclusive of 0 for log)
	T1S    float64 // window end, seconds
}

// FitResult holds the outcome of a sqrt-time or log-time fit on one segment.
type FitResult struct {
	Method  string
	D0      float64 // fitted initial dial, mm
	D100    float64 // fitted 100% primary dial, mm
	T50S    float64 // seconds, log method only (0 otherwise)
	T90S    float64 // seconds, sqrt method only (0 otherwise)
	CvM2Yr  float64 // coefficient of consolidation
	DrainMM float64 // drainage path length used
	Points  int     // samples inside the window
}

const secPerYear = 365.25 * 24 * 3600

// FitSegment runs the requested method on one segment. The window selects
// the straight-line portion; readings outside it never influence the fit.
func FitSegment(seg Segment, w FitWindow, drainMM float64) (FitResult, error) {
	if drainMM <= 0 {
		return FitResult{}, fmt.Errorf("drainage path must be positive")
	}
	var xs, ys []float64
	for _, r := range seg.Readings {
		t := r.ElapsedS
		if t < w.T0S || t > w.T1S {
			continue
		}
		switch w.Method {
		case "sqrt":
			xs = append(xs, math.Sqrt(t))
		case "log":
			if t <= 0 {
				continue
			}
			xs = append(xs, math.Log10(t))
		default:
			return FitResult{}, fmt.Errorf("unknown method %q", w.Method)
		}
		ys = append(ys, r.DialMM)
	}
	if len(xs) < 2 {
		return FitResult{}, fmt.Errorf("window selects %d points, need >=2", len(xs))
	}
	ln, err := FitLine(xs, ys)
	if err != nil {
		return FitResult{}, err
	}
	res := FitResult{Method: w.Method, DrainMM: drainMM, Points: len(xs)}
	dStart := seg.Readings[0].DialMM
	dEnd := seg.Readings[len(seg.Readings)-1].DialMM
	switch w.Method {
	case "sqrt":
		d0 := ln.A
		d90 := d0 + 0.9*(dEnd-d0)
		// Taylor construction: the curve reaches d90 where sqrt(t) is
		// 1.15x the straight-line extrapolation.
		t90 := math.Pow(1.15*(d90-ln.A)/ln.B, 2)
		res.D0, res.D100, res.T90S = d0, dEnd, t90
		res.CvM2Yr = 0.848 * math.Pow(drainMM/1000, 2) / t90 * secPerYear
	case "log":
		d0 := dStart
		d100 := dEnd
		d50 := (d0 + d100) / 2
		t50 := math.Pow(10, (d50-ln.A)/ln.B)
		res.D0, res.D100, res.T50S = d0, d100, t50
		res.CvM2Yr = 0.197 * math.Pow(drainMM/1000, 2) / t50 * secPerYear
	}
	return res, nil
}

// --- Casagrande construction ---

// CurvatureCandidate is a user-picked point of maximum curvature on the
// e-log(p) curve, kept in data coordinates.
type CurvatureCandidate struct {
	ID   int
	LogP float64
	E    float64
	Note string
}

// CasagrandeResult is the geometric construction for one candidate.
type CasagrandeResult struct {
	Candidate  CurvatureCandidate
	Bisector   Line // angle bisector through the candidate
	VirginLine Line // fitted virgin compression line
	PcKPa      float64
}

// Casagrande performs the geometric construction for one curvature
// candidate against the first-loading branch. The tangent at the candidate
// is estimated from neighbouring branch points; the virgin line is fitted
// on branch points above the candidate pressure.
func Casagrande(c CurvatureCandidate, branch []ELogPPoint) (CasagrandeResult, error) {
	if len(branch) < 3 {
		return CasagrandeResult{}, fmt.Errorf("first-loading branch needs >=3 points, got %d", len(branch))
	}
	// Local tangent: slope between the branch points bracketing the candidate.
	var before, after *ELogPPoint
	for i := range branch {
		if branch[i].LogP <= c.LogP {
			before = &branch[i]
		}
		if branch[i].LogP >= c.LogP && after == nil {
			after = &branch[i]
		}
	}
	if before == nil || after == nil || before == after {
		return CasagrandeResult{}, fmt.Errorf("candidate outside first-loading branch")
	}
	mTan := (after.VoidRatio - before.VoidRatio) / (after.LogP - before.LogP)

	// Virgin compression line: branch points above the candidate pressure.
	var xs, ys []float64
	for _, p := range branch {
		if p.LogP > c.LogP {
			xs = append(xs, p.LogP)
			ys = append(ys, p.VoidRatio)
		}
	}
	if len(xs) < 2 { // fall back to the two highest branch points
		xs = []float64{branch[len(branch)-2].LogP, branch[len(branch)-1].LogP}
		ys = []float64{branch[len(branch)-2].VoidRatio, branch[len(branch)-1].VoidRatio}
	}
	virgin, err := FitLine(xs, ys)
	if err != nil {
		return CasagrandeResult{}, err
	}

	// Angle bisector between the horizontal and the tangent through the
	// candidate, in (logP, e) coordinates.
	theta := math.Atan(mTan) / 2
	bisector := Line{A: c.E - math.Tan(theta)*c.LogP, B: math.Tan(theta)}

	// Intersection of bisector with the virgin line gives pc.
	den := bisector.B - virgin.B
	if math.Abs(den) < 1e-12 {
		return CasagrandeResult{}, fmt.Errorf("bisector parallel to virgin line")
	}
	logPc := (virgin.A - bisector.A) / den
	return CasagrandeResult{
		Candidate: c, Bisector: bisector, VirginLine: virgin,
		PcKPa: math.Pow(10, logPc),
	}, nil
}

// PcRange evaluates all candidates and returns the admissible pc range.
// Candidates whose construction fails are reported but do not block others.
func PcRange(cands []CurvatureCandidate, branch []ELogPPoint) (results []CasagrandeResult, lo, hi float64, errs []error) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, c := range cands {
		r, err := Casagrande(c, branch)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		results = append(results, r)
		lo = math.Min(lo, r.PcKPa)
		hi = math.Max(hi, r.PcKPa)
	}
	return results, lo, hi, errs
}
