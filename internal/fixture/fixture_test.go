package fixture

import (
	"testing"

	"consolidation/internal/consol"
)

// TestFixtureAnomalies confirms the planted gauge reset and clock rollback
// are detected by segmentation, and the load path keeps its true order.
func TestFixtureAnomalies(t *testing.T) {
	run := Build()
	if len(run.Stages) != 13 {
		t.Fatalf("stages = %d", len(run.Stages))
	}
	// group readings per stage
	byStage := map[int][]consol.Reading{}
	for _, r := range run.Readings {
		byStage[r.Stage] = append(byStage[r.Stage], r)
	}
	segs6 := consol.SplitSegments(6, byStage[6])
	if len(segs6) != 2 || segs6[1].Flags[0] != "gauge-reset" {
		t.Fatalf("stage 6 segments = %+v", segs6)
	}
	segs3 := consol.SplitSegments(3, byStage[3])
	if len(segs3) != 2 || segs3[1].Flags[0] != "clock-rollback" {
		t.Fatalf("stage 3 segments = %+v", segs3)
	}
	// corrected cumulative displacement stays continuous across the reset
	pts := consol.Correct(run.Readings, run.Specimen)
	for i := 1; i < len(pts); i++ {
		if pts[i].CumDialMM < pts[i-1].CumDialMM-1.0 {
			t.Fatalf("cum dial jumps at seq %d: %v -> %v", i, pts[i-1].CumDialMM, pts[i].CumDialMM)
		}
	}
	// true path order: unload dips below first-loading maximum
	path := consol.LoadPath(run.Stages, pts)
	var pressures []float64
	for _, p := range path {
		pressures = append(pressures, p.PressureKPa)
	}
	want := []float64{0, 12.5, 25, 50, 100, 200, 400, 100, 25, 100, 200, 400, 800}
	if len(pressures) != len(want) {
		t.Fatalf("path = %v", pressures)
	}
	for i := range want {
		if pressures[i] != want[i] {
			t.Fatalf("path = %v, want %v", pressures, want)
		}
	}
	// zero-load state skipped from log axis but preserved
	elogp, skipped := consol.ELogP(path)
	if len(skipped) != 1 || skipped[0].PressureKPa != 0 {
		t.Fatalf("skipped = %+v", skipped)
	}
	for _, p := range elogp {
		if p.LogP < 0 && p.PressureKPa < 1 {
			t.Fatalf("unexpected: %+v", p)
		}
	}
	// first-loading branch has exactly the 6 virgin points
	branch := consol.FirstLoadingBranch(elogp)
	if len(branch) != 6 {
		t.Fatalf("branch = %d points", len(branch))
	}
}
