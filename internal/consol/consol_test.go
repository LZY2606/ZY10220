package consol

import (
	"math"
	"testing"
)

func spec() Specimen { return Specimen{Height0MM: 20, VoidRatio0: 1.0, DoubleDrain: true} }

// TestSplitSegmentsReset splits a stage at a planted gauge reset.
func TestSplitSegmentsReset(t *testing.T) {
	rs := []Reading{
		{Seq: 0, ElapsedS: 0, DialMM: 1.0},
		{Seq: 1, ElapsedS: 60, DialMM: 1.5},
		{Seq: 2, ElapsedS: 120, DialMM: 0.1, Note: "RESET"},
		{Seq: 3, ElapsedS: 240, DialMM: 0.4},
	}
	segs := SplitSegments(6, rs)
	if len(segs) != 2 {
		t.Fatalf("want 2 segments, got %d", len(segs))
	}
	if len(segs[1].Flags) == 0 || segs[1].Flags[0] != "gauge-reset" {
		t.Fatalf("segment 1 flags = %v", segs[1].Flags)
	}
}

// TestSplitSegmentsRollback splits a stage at a clock rollback.
func TestSplitSegmentsRollback(t *testing.T) {
	rs := []Reading{
		{Seq: 0, ElapsedS: 0, DialMM: 1.0},
		{Seq: 1, ElapsedS: 900, DialMM: 1.5},
		{Seq: 2, ElapsedS: 600, DialMM: 1.6}, // clock went backwards
		{Seq: 3, ElapsedS: 1200, DialMM: 1.7},
	}
	segs := SplitSegments(3, rs)
	if len(segs) != 2 || segs[1].Flags[0] != "clock-rollback" {
		t.Fatalf("segments = %+v", segs)
	}
}

// TestCorrectCompensatesReset keeps cumulative displacement continuous
// across a gauge reset without touching raw readings.
func TestCorrectCompensatesReset(t *testing.T) {
	rs := []Reading{
		{Seq: 0, ElapsedS: 0, DialMM: 2.0},
		{Seq: 1, ElapsedS: 60, DialMM: 2.5},
		{Seq: 2, ElapsedS: 120, DialMM: 0.0, Note: "RESET"},
		{Seq: 3, ElapsedS: 240, DialMM: 0.3},
	}
	pts := Correct(rs, spec())
	if pts[2].CumDialMM != pts[1].CumDialMM {
		t.Fatalf("reset not compensated: cum %v -> %v", pts[1].CumDialMM, pts[2].CumDialMM)
	}
	if math.Abs(pts[3].CumDialMM-2.8) > 1e-9 {
		t.Fatalf("cum after reset = %v, want 2.8", pts[3].CumDialMM)
	}
	// raw dial untouched
	if pts[2].Raw.DialMM != 0 {
		t.Fatalf("raw mutated: %v", pts[2].Raw.DialMM)
	}
	// void ratio decreases under compression
	if pts[3].VoidRatio >= pts[0].VoidRatio {
		t.Fatalf("void ratio not decreasing: %v -> %v", pts[0].VoidRatio, pts[3].VoidRatio)
	}
}

// TestLoadPathKeepsTrueOrder verifies unload/reload branches stay in record
// order and are never re-sorted by pressure.
func TestLoadPathKeepsTrueOrder(t *testing.T) {
	stages := []Stage{
		{No: 0, PressureKPa: 0, Kind: KindInitial, StartSeq: 0, EndSeq: 0},
		{No: 1, PressureKPa: 100, Kind: KindLoad, StartSeq: 1, EndSeq: 1},
		{No: 2, PressureKPa: 400, Kind: KindLoad, StartSeq: 2, EndSeq: 2},
		{No: 3, PressureKPa: 100, Kind: KindUnload, StartSeq: 3, EndSeq: 3},
		{No: 4, PressureKPa: 400, Kind: KindReload, StartSeq: 4, EndSeq: 4},
	}
	rs := make([]Reading, 5)
	for i := range rs {
		rs[i] = Reading{Seq: i, DialMM: float64(i)}
	}
	path := LoadPath(stages, Correct(rs, spec()))
	got := []float64{}
	for _, p := range path {
		got = append(got, p.PressureKPa)
	}
	want := []float64{0, 100, 400, 100, 400}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("path order = %v, want %v", got, want)
		}
	}
	pts, skipped := ELogP(path)
	if len(skipped) != 1 || skipped[0].PressureKPa != 0 {
		t.Fatalf("zero-load state not preserved in skipped: %+v", skipped)
	}
	for _, p := range pts {
		if p.PressureKPa <= 0 {
			t.Fatalf("non-positive pressure reached log axis: %v", p.PressureKPa)
		}
	}
	branch := FirstLoadingBranch(pts)
	if len(branch) != 2 || branch[0].PressureKPa != 100 || branch[1].PressureKPa != 400 {
		t.Fatalf("first-loading branch contaminated: %+v", branch)
	}
}

// TestFitSqrtRecoversCv feeds a synthetic Terzaghi curve with known cv and
// checks the sqrt-time fit gets it back.
func TestFitSqrtRecoversCv(t *testing.T) {
	const cvMM2S = 2.0 * 1e6 / (365.25 * 24 * 3600) // 2 m^2/yr
	const drain = 10.0                              // mm
	u := func(tv float64) float64 {
		if tv < 0.2827 {
			return math.Sqrt(4 * tv / math.Pi)
		}
		return 1 - 8/(math.Pi*math.Pi)*math.Exp(-math.Pi*math.Pi*tv/4)
	}
	var seg Segment
	for _, t := range []float64{15, 30, 60, 120, 240, 480, 900, 1800, 3600, 7200, 14400} {
		tv := cvMM2S * t / (drain * drain)
		seg.Readings = append(seg.Readings, Reading{ElapsedS: t, DialMM: u(tv) * 1.0})
	}
	// window over the early straight portion (U < ~0.6)
	res, err := FitSegment(seg, FitWindow{Method: "sqrt", T0S: 0, T1S: 900}, drain)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.CvM2Yr-2.0) > 0.3 {
		t.Fatalf("cv = %v, want ~2.0", res.CvM2Yr)
	}
	res2, err := FitSegment(seg, FitWindow{Method: "log", T0S: 15, T1S: 14400}, drain)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res2.CvM2Yr-2.0) > 0.6 {
		t.Fatalf("log-method cv = %v, want ~2.0", res2.CvM2Yr)
	}
}

// TestCasagrandeRange builds an idealised e-log(p) curve with a known break
// at p=100 and checks the construction recovers it, and that multiple
// candidates yield a range.
func TestCasagrandeRange(t *testing.T) {
	mk := func(p float64) ELogPPoint {
		var e float64
		if p <= 100 {
			e = 0.9 - 0.05*math.Log10(p/10)
		} else {
			e = 0.85 - 0.3*math.Log10(p/100)
		}
		return ELogPPoint{PathPoint: PathPoint{PressureKPa: p, Kind: KindLoad, VoidRatio: e}, LogP: math.Log10(p)}
	}
	branch := []ELogPPoint{mk(10), mk(25), mk(50), mk(75), mk(100), mk(200), mk(400), mk(800)}
	c1 := CurvatureCandidate{ID: 1, LogP: math.Log10(85), E: 0.9 - 0.05*math.Log10(85/10)}
	c2 := CurvatureCandidate{ID: 2, LogP: math.Log10(95), E: 0.9 - 0.05*math.Log10(95/10)}
	res, lo, hi, errs := PcRange([]CurvatureCandidate{c1, c2}, branch)
	if len(errs) > 0 {
		t.Fatalf("errs: %v", errs)
	}
	if len(res) != 2 {
		t.Fatalf("results = %d", len(res))
	}
	// The geometric construction is approximate; accept 15% around the
	// planted break at p=100 and require a non-degenerate range.
	if lo < 85 || hi > 115 {
		t.Fatalf("pc range [%v, %v] too far from true pc=100", lo, hi)
	}
	if hi-lo <= 0 {
		t.Fatalf("expected a non-degenerate range, got [%v, %v]", lo, hi)
	}
}
