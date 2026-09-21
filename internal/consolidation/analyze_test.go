package consolidation

import (
	"math"
	"testing"

	"consolidation-console/internal/domain"
	"consolidation-console/internal/fixture"
)

func analyzeFixture(t *testing.T, chosen int) Result {
	t.Helper()
	run, steps, readings, cfgs := fixture.Build()
	cfg := map[int]domain.StepConfig{}
	for _, c := range cfgs {
		cfg[c.Step] = c
	}
	return Analyze(run, steps, readings, cfg, nil, chosen)
}

func TestFixtureDeterministic(t *testing.T) {
	_, _, r1, _ := fixture.Build()
	_, _, r2, _ := fixture.Build()
	if len(r1) != len(r2) {
		t.Fatalf("fixture length differs: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("fixture not deterministic at %d: %+v vs %+v", i, r1[i], r2[i])
		}
	}
}

func TestLoadPathKeptUnsorted(t *testing.T) {
	res := analyzeFixture(t, -1)
	// 路径顺序必须是 25..800 加载，再 400..50 卸载，再 100..800 再加载。
	var seq []float64
	var phases []string
	for _, s := range res.StepResults {
		seq = append(seq, s.Pressure)
		phases = append(phases, s.Phase)
	}
	wantP := []float64{25, 50, 100, 200, 400, 800, 400, 200, 100, 50, 100, 200, 400, 800}
	if len(seq) != len(wantP) {
		t.Fatalf("step count=%d want %d", len(seq), len(wantP))
	}
	for i := range wantP {
		if seq[i] != wantP[i] {
			t.Fatalf("path order changed at %d: got %v", i, seq)
		}
	}
	// 卸载段压力单调下降，不能按压力排序混入首次加载。
	unload := res.Casa.Unload
	if len(unload) != 4 {
		t.Fatalf("unload points=%d want 4", len(unload))
	}
	for i := 1; i < len(unload); i++ {
		if unload[i].Pressure >= unload[i-1].Pressure {
			t.Fatalf("unload must decrease: %v", unload)
		}
	}
	if phases[6] != domain.PhaseUnload || phases[10] != domain.PhaseReload {
		t.Fatalf("phase labels wrong: %v", phases)
	}
	// 首次加载曲线独立。
	if len(res.Casa.FirstLoad) != 6 {
		t.Fatalf("first-load points=%d want 6", len(res.Casa.FirstLoad))
	}
	if res.Casa.Initial.Pressure != 0 {
		t.Fatalf("zero-load initial state must remain, got pressure %v", res.Casa.Initial.Pressure)
	}
}

func TestResetSegmentationAndTraceability(t *testing.T) {
	res := analyzeFixture(t, -1)
	// 第 5 级首条读数是归零点。
	var reset Point
	var after Point
	found := 0
	for _, p := range res.Points {
		if p.Step == 5 {
			if p.Reset {
				reset = p
				found++
			}
			if found == 1 && !p.Reset && p.ClockMin == 0.5 {
				after = p
				found++
			}
		}
	}
	if reset.ReadingID == 0 {
		t.Fatal("reset reading not found")
	}
	if reset.GaugeMm != 0 {
		t.Fatalf("reset raw gauge must be 0, got %f", reset.GaugeMm)
	}
	// 归零单点独立成段，后续读数在新段。
	if reset.Segment != 0 || after.Segment != 1 {
		t.Fatalf("reset segmentation: reset seg=%d after seg=%d", reset.Segment, after.Segment)
	}
	// 物理压缩必须跨归零连续：归零点=前级末值，后续点=该值+新读数。
	prevEnd := 0.0
	for _, s := range res.StepResults {
		if s.Step == 4 {
			prevEnd = s.EndSmm
		}
	}
	if math.Abs(reset.Settlement-prevEnd) > 2e-3 {
		t.Fatalf("reset physical settlement not continuous: %f vs prev end %f",
			reset.Settlement, prevEnd)
	}
	if after.Settlement <= prevEnd {
		t.Fatalf("post-reset settlement should continue increasing: %f", after.Settlement)
	}
	// 原始读数仍然保留且可反查（归零读数 gauge=0，未被改写）。
	if reset.Diagnostic == "" {
		t.Fatal("reset point should carry diagnostic note")
	}
}

func TestClockRollbackSegmentation(t *testing.T) {
	res := analyzeFixture(t, -1)
	var bad Point
	n := 0
	for _, p := range res.Points {
		if p.Step == 3 && p.Rollback {
			bad = p
			n++
		}
	}
	if n != 1 {
		t.Fatalf("rollback points=%d want 1", n)
	}
	// 回退点自成一段，且不允许按其自身表读数推算趋势：
	// 若错误地按健康公式计算，会得到与时钟 7min 对应的“伪压缩”值。
	var lastHealthy Point
	var fabricatedFromGauge float64 = -1
	for _, p := range res.Points {
		if p.Step == 3 && !p.Rollback {
			lastHealthy = p
			// 健康公式：基准 0.368 - (gauge - 首读 -0.368)
			if p.ClockMin == 7 {
				fabricatedFromGauge = p.Settlement
			}
		}
	}
	if bad.Segment == lastHealthy.Segment {
		t.Fatal("rollback reading must start its own segment")
	}
	if math.Abs(bad.Settlement-lastHealthy.Settlement) > 2e-3 {
		t.Fatalf("rollback point must carry last healthy settlement, not extrapolate: bad=%f last=%f",
			bad.Settlement, lastHealthy.Settlement)
	}
	if fabricatedFromGauge > 0 && math.Abs(bad.Settlement-fabricatedFromGauge) < 1e-3 {
		t.Fatal("rollback point settlement must not be computed from its own gauge")
	}
	// 该级 fixture 中回退点是最后一条读数，因此是 2 段：前健康段 + 回退孤立段。
	// 关键是回退段被显式标记且不参与拟合，不与前段拼接。
	for _, s := range res.StepResults {
		if s.Step == 3 {
			if len(s.Segments) < 2 {
				t.Fatalf("step3 segments=%d want >=2", len(s.Segments))
			}
			var sawRollback bool
			for _, seg := range s.Segments {
				if seg.Rollback {
					sawRollback = true
				}
			}
			if !sawRollback {
				t.Fatal("step3 must contain a flagged rollback segment")
			}
		}
	}
	// 拟合不能选到回退段。
	for _, s := range res.StepResults {
		if s.Step == 3 {
			for _, seg := range s.Segments {
				if seg.Index == s.FitSegment && seg.Rollback {
					t.Fatal("fit must not use rollback segment")
				}
			}
		}
	}
}

func TestConsolidationCoefficients(t *testing.T) {
	res := analyzeFixture(t, -1)
	for _, s := range res.StepResults {
		if s.Phase != domain.PhaseLoad {
			continue
		}
		if s.CvCm2Min <= 0 {
			t.Fatalf("loading step %d cv must be positive, got %f", s.Step, s.CvCm2Min)
		}
		if s.Fit == nil || s.Fit.T90Min <= 0 || s.Fit.T90Min > 120 {
			t.Fatalf("step %d t90 out of range: %+v", s.Step, s.Fit)
		}
	}
	// 卸载回弹不应产生正的压缩增量。
	for _, s := range res.StepResults {
		if s.Phase == domain.PhaseUnload && s.DeltaSmm >= 0 {
			t.Fatalf("unload step %d deltaS must be negative: %f", s.Step, s.DeltaSmm)
		}
	}
}

func TestCasagrandeCandidates(t *testing.T) {
	res := analyzeFixture(t, -1)
	c := res.Casa
	if len(c.Candidates) < 2 {
		t.Fatalf("should retain multiple curvature candidates, got %d", len(c.Candidates))
	}
	if !(c.PcLow > 50 && c.PcHigh < 250 && c.PcLow < c.PcHigh) {
		t.Fatalf("pc range unexpected: %.0f..%.0f", c.PcLow, c.PcHigh)
	}
	if !(c.PcAuto >= c.PcLow && c.PcAuto <= c.PcHigh) {
		t.Fatalf("auto pc %.0f outside range %.0f..%.0f", c.PcAuto, c.PcLow, c.PcHigh)
	}
	// 人工选择另一个候选。
	res2 := analyzeFixture(t, 1)
	if math.Abs(res2.Casa.PcChosen-res2.Casa.Candidates[0].PcKPa) > 1e-6 {
		t.Fatalf("manual candidate not applied: %+v", res2.Casa)
	}
}

func TestVoidRatioThickness(t *testing.T) {
	res := analyzeFixture(t, -1)
	// 首次加载 e 单调下降，厚度同步下降。
	var prevE, prevH float64 = 1e9, 1e9
	for _, s := range res.StepResults {
		if s.Phase != domain.PhaseLoad {
			continue
		}
		if s.EEnd > prevE+1e-9 {
			t.Fatalf("e must decrease on first load: step %d %f > %f", s.Step, s.EEnd, prevE)
		}
		if s.HEndCm > prevH+1e-9 {
			t.Fatalf("thickness must decrease on first load: step %d", s.Step)
		}
		if math.Abs((res.Run.E0-voidCoeff(res.Run)*s.EndSmm)-s.EEnd) > 1e-9 {
			t.Fatalf("e not consistent with settlement, step %d", s.Step)
		}
		prevE, prevH = s.EEnd, s.HEndCm
	}
}

func TestOverrideMovesBoundary(t *testing.T) {
	run, steps, readings, cfgs := fixture.Build()
	cfg := map[int]domain.StepConfig{}
	for _, c := range cfgs {
		cfg[c.Step] = c
	}
	// 把回退读数 #37 从第 3 级改派到第 4 级：原始 OrigStep 仍为 3。
	res := Analyze(run, steps, readings, cfg, map[int64]int{37: 4}, -1)
	var moved int
	for _, p := range res.Points {
		if p.ReadingID == 37 {
			if p.OrigStep != 3 || p.Step != 4 {
				t.Fatalf("override mismatch: orig=%d step=%d", p.OrigStep, p.Step)
			}
			if p.Pressure != 200 {
				t.Fatalf("overridden reading should take new step pressure, got %f", p.Pressure)
			}
			moved++
		}
	}
	if moved != 1 {
		t.Fatalf("overridden reading count=%d", moved)
	}
}

func TestLogMethodProducesT50(t *testing.T) {
	run, steps, readings, _ := fixture.Build()
	cfg := map[int]domain.StepConfig{}
	for i := range steps {
		cfg[i+1] = domain.StepConfig{Step: i + 1, Method: "log", WindowStartMin: 1, WindowEndMin: 25}
	}
	res := Analyze(run, steps, readings, cfg, nil, -1)
	s1 := res.StepResults[0]
	if s1.Fit == nil || s1.Fit.T50Min <= 0 {
		t.Fatalf("log method should yield t50: %+v", s1.Fit)
	}
	if s1.CvCm2Min <= 0 {
		t.Fatalf("log method cv should be positive, got %f", s1.CvCm2Min)
	}
}
