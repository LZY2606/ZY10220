// Package fixture builds the deterministic demonstration run: a full
// load-unload-reload consolidation test with a mid-stage gauge reset and
// one clock rollback. Everything is computed from closed-form formulas so
// tests and replays are byte-stable.
package fixture

import (
	"math"

	"consolidation/internal/consol"
)

// Run is the in-memory form of one consolidation run before persistence.
type Run struct {
	Name     string
	Specimen consol.Specimen
	Stages   []consol.Stage
	Readings []consol.Reading
}

// reading times within a stage, minutes.
var stageTimesMin = []float64{0, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60, 120, 240}

// cv in mm^2/s (2.0 m^2/yr), drainage path 10 mm (double drainage, H0=20).
const cvMM2S = 2.0 * 1e6 / (365.25 * 24 * 3600)

// terzaghiU approximates the average degree of consolidation.
func terzaghiU(tv float64) float64 {
	if tv <= 0 {
		return 0
	}
	if tv < 0.2827 {
		return math.Sqrt(4 * tv / math.Pi)
	}
	return 1 - 8/(math.Pi*math.Pi)*math.Exp(-math.Pi*math.Pi*tv/4)
}

// stagePlan describes one planned load stage.
type stagePlan struct {
	pressure float64
	kind     consol.StageKind
	index    float64 // Cc or Cs governing this stage
}

// Build generates the deterministic fixture run.
//
// Path: 0 -> 12.5 -> 25 -> 50 -> 100 -> 200 -> 400 (first loading, gauge
// reset inside the 400 kPa stage) -> 100 -> 25 (unload) -> 100 -> 200 ->
// 400 -> 800 (reload, extending the virgin line). A clock rollback is
// planted inside the 50 kPa stage.
func Build() Run {
	spec := consol.Specimen{Height0MM: 20, VoidRatio0: 1.0, DoubleDrain: true}
	const cc, cs = 0.35, 0.05
	plans := []stagePlan{
		{0, consol.KindInitial, 0},
		{12.5, consol.KindLoad, cc},
		{25, consol.KindLoad, cc},
		{50, consol.KindLoad, cc},
		{100, consol.KindLoad, cc},
		{200, consol.KindLoad, cc},
		{400, consol.KindLoad, cc},
		{100, consol.KindUnload, cs},
		{25, consol.KindUnload, cs},
		{100, consol.KindReload, cs},
		{200, consol.KindReload, cs},
		{400, consol.KindReload, cs},
		{800, consol.KindReload, cc},
	}

	run := Run{Name: "FIX-01 固结路径演示", Specimen: spec}
	seq := 0
	e := spec.VoidRatio0
	height := spec.Height0MM
	cumDial := 0.0
	prevP := 1.0 // log reference for the first load step
	resetOffset := 0.0

	for stNo, pl := range plans {
		stage := consol.Stage{No: stNo, PressureKPa: pl.pressure, Kind: pl.kind, StartSeq: seq}
		var stageSettle float64
		if pl.pressure > 0 {
			de := pl.index * math.Log10(pl.pressure/prevP)
			if pl.kind == consol.KindUnload {
				de = -pl.index * math.Log10(prevP/pl.pressure)
			}
			stageSettle = de / (1 + e) * height
			e -= de
			height -= stageSettle
			prevP = pl.pressure
		}
		hdr := height / 2
		for ti, tMin := range stageTimesMin {
			t := tMin * 60
			elapsed := t
			// Planted clock rollback inside stage 3 (50 kPa): the clock
			// jumps backwards at the 30-minute reading and keeps counting
			// from the rolled-back value.
			if stNo == 3 && tMin >= 30 {
				elapsed = t - 20*60
			}
			tv := cvMM2S * t / (hdr * hdr)
			dial := cumDial + terzaghiU(tv)*stageSettle - resetOffset
			note := ""
			// Planted gauge reset inside stage 6 (400 kPa first loading):
			// after the 15-minute reading the gauge is re-zeroed.
			if stNo == 6 && tMin == 30 {
				resetOffset = cumDial + terzaghiU(tv)*stageSettle
				dial = 0
				note = "RESET"
			}
			if stNo == 6 && tMin > 30 {
				dial = cumDial + terzaghiU(tv)*stageSettle - resetOffset
			}
			run.Readings = append(run.Readings, consol.Reading{
				Seq: seq, Stage: stNo, PressureKPa: pl.pressure,
				ElapsedS: elapsed, DialMM: round6(dial), Note: note,
			})
			seq++
			_ = ti
		}
		cumDial += stageSettle
		stage.EndSeq = seq - 1
		run.Stages = append(run.Stages, stage)
	}
	return run
}

func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }
