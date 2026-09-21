// Package consolidation 按“原始读数 / 修正值 / 绘图几何 / 人工选择”四层组织计算。
// 全部为值计算，绝不修改 domain.Reading 原始读数。
package consolidation

import (
	"sort"

	"consolidation-console/internal/domain"
)

// Point 是修正链上的一个读数点。
type Point struct {
	ReadingID  int64
	OrigStep   int
	Step       int
	Pressure   float64 // kPa
	Phase      string
	ClockMin   float64 // 仪器时钟（本级）
	StepTime   float64 // 相对本级开始的序列时间（供趋势轴）
	GaugeMm    float64 // 原始读数
	Settlement float64 // 修正后物理累计压缩(mm)，压缩为正
	Reset      bool
	Rollback   bool
	Segment    int
	Diagnostic string
}

// Segment 是同一级内的连续片段（归零或时钟回退会切断）。
type Segment struct {
	Step       int
	Index      int
	Count      int
	HasReset   bool
	Rollback   bool
	StartClock float64
	EndClock   float64
	Note       string
}

type FitWindow struct {
	Method   string
	StartMin float64
	EndMin   float64
}

// FitGeometry 保存拟合直线与特征点（绘图几何层）。
type FitGeometry struct {
	Method string
	// 拟合直线在其变换横轴上的两点（sqrt: √min；log: log10 min）
	X1, Y1 float64
	X2, Y2 float64
	D0     float64
	D50    float64
	D90    float64
	D100   float64
	T50Min float64
	T90Min float64
	R2     float64
	Window FitWindow
	Note   string
}

type StepResult struct {
	Step       int
	Pressure   float64
	Phase      string
	StartSmm   float64
	EndSmm     float64
	DeltaSmm   float64
	HStartCm   float64
	HEndCm     float64
	EStart     float64
	EEnd       float64
	CvCm2Min   float64
	CvCm2Sec   float64
	DrainCm    float64
	Segments   []Segment
	FitSegment int
	Fit        *FitGeometry
	Diagnostic string
}

type CurvePoint struct {
	Step      int
	Pressure  float64
	E         float64
	Phase     string
	PathIndex int
}

type Candidate struct {
	Index        int
	Step         int
	Pressure     float64
	E            float64
	Curvature    float64
	TurnDeg      float64
	PcKPa        float64
	GeometryOK   bool
	LocalMaximum bool
}

type CasagrandeResult struct {
	FirstLoad   []CurvePoint
	Unload      []CurvePoint
	Reload      []CurvePoint
	Initial     CurvePoint
	Candidates  []Candidate
	VirginSlope float64
	VirginInter float64
	PcLow       float64
	PcHigh      float64
	PcAuto      float64
	PcChosen    float64
	ChosenIndex int
	Note        string
}

type Result struct {
	Run         domain.Run
	Steps       []domain.StepSpec
	Points      []Point
	StepResults []StepResult
	Casa        CasagrandeResult
}

// voidCoeff：de/ds=(1+e0)/H0，s 用 mm，H0 用 cm。
func voidCoeff(run domain.Run) float64 { return (1 + run.E0) / (run.H0Cm * 10) }

type segBuilder struct {
	segments []Segment
	cur      *Segment
}

func (b *segBuilder) open(step int, idx int, clock float64, reset, rollback bool) *Segment {
	s := &Segment{Step: step, Index: idx, StartClock: clock, EndClock: clock,
		HasReset: reset, Rollback: rollback}
	if rollback {
		s.Note = "时钟回退片段：不与前后拼接，不参与拟合"
	} else if reset {
		s.Note = "位移计归零片段：按重置标记续接物理压缩"
	}
	b.segments = append(b.segments, *s)
	b.cur = s
	return s
}

func (b *segBuilder) add(clock float64) {
	if b.cur == nil {
		return
	}
	b.cur.Count++
	b.cur.EndClock = clock
}

// Analyze 执行全链：级次改派、归零与回退分段、修正累计压缩、
// 各级固结参数、e-logp 路径点、Casagrande 候选。
func Analyze(run domain.Run, steps []domain.StepSpec, readings []domain.Reading,
	configs map[int]domain.StepConfig, overrides map[int64]int, chosenCurve int) Result {

	res := Result{Run: run, Steps: steps}
	pressureOf := map[int]float64{}
	phaseOf := map[int]string{}
	for _, st := range steps {
		pressureOf[st.Seq] = st.PressureKPa
		phaseOf[st.Seq] = st.Phase
	}

	byStep := map[int][]domain.Reading{}
	for _, rd := range readings {
		step := rd.Seq
		if ns, ok := overrides[rd.ID]; ok {
			step = ns
		}
		byStep[step] = append(byStep[step], rd)
	}

	coeff := voidCoeff(run)
	runningS := 0.0 // 真实路径序列上的物理累计压缩

	for _, st := range steps {
		rs := append([]domain.Reading(nil), byStep[st.Seq]...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
		if len(rs) == 0 {
			continue
		}
		startS := runningS
		resetBase := startS // 当前归零基准对应的物理累计压缩
		firstGauge := 0.0   // 当前段起点的原始表读数
		var lastClock float64
		var lastPhys = startS
		healthyPrev := startS
		b := &segBuilder{}
		segIndex := -1
		var curSeg *Segment
		prevWasRollback := false

		for idx, rd := range rs {
			diag := ""
			rollback := false
			if idx == 0 {
				segIndex++
				curSeg = b.open(st.Seq, segIndex, rd.ClockMin, rd.Reset, false)
				firstGauge = rd.GaugeMm
				if rd.Reset {
					// 归零动作发生在本级起点：表读数从 0 重新记起，物理压缩继承序列前级末值。
					firstGauge = 0
					diag = "位移计归零后读数，已按重置标记续接"
				}
			} else if rd.Reset {
				segIndex++
				curSeg = b.open(st.Seq, segIndex, rd.ClockMin, true, false)
				// 归零后表读数从 0 重新记起，物理基准 = 归零时刻的真实压缩。
				resetBase = lastPhys
				firstGauge = rd.GaugeMm // 归零读数本身为 0
				diag = "位移计归零后读数，已按重置标记续接"
			} else if rd.ClockMin < lastClock {
				rollback = true
				segIndex++
				curSeg = b.open(st.Seq, segIndex, rd.ClockMin, false, true)
				diag = curSeg.Note
			} else if prevWasRollback {
				segIndex++
				curSeg = b.open(st.Seq, segIndex, rd.ClockMin, false, false)
				// 回退结束：后续读数沿原健康段基准继续（firstGauge 不变）。
				diag = "时钟回退后恢复段：不与回退点拼接"
			}

			if !rollback && !rd.Reset && idx > 0 && rs[idx-1].Reset {
				// 归零读数自成一段；后续读数进入“归零后”新段。
				segIndex++
				curSeg = b.open(st.Seq, segIndex, rd.ClockMin, true, false)
				firstGauge = rs[idx-1].GaugeMm // 归零读数=0
			}
			var phys float64
			if rollback {
				// 回退点不做物理外推：显示为回退发生前最后健康读数的累计值，
				// 原读数保留可反查，但不拼接成新趋势。
				phys = healthyPrev
			} else {
				phys = resetBase - (rd.GaugeMm - firstGauge)
				lastPhys = phys
				healthyPrev = phys
			}
			b.add(rd.ClockMin)
			p := pressureOf[st.Seq]
			if ns, ok := overrides[rd.ID]; ok {
				p = pressureOf[ns]
			}
			res.Points = append(res.Points, Point{
				ReadingID: rd.ID, OrigStep: rd.OrigStep, Step: st.Seq,
				Pressure: p, Phase: phaseOf[st.Seq], ClockMin: rd.ClockMin,
				GaugeMm: rd.GaugeMm, Settlement: phys, Reset: rd.Reset,
				Rollback: rollback, Segment: segIndex, Diagnostic: diag,
			})
			lastClock = rd.ClockMin
			prevWasRollback = rollback
		}
		endS := lastPhys
		runningS = endS

		fitSeg := longestHealthySegment(res.Points, st.Seq)
		sr := StepResult{Step: st.Seq, Pressure: st.PressureKPa, Phase: st.Phase,
			Segments: b.segments, FitSegment: fitSeg}
		sr.StartSmm = startS
		sr.EndSmm = endS
		sr.DeltaSmm = endS - startS
		sr.HStartCm = run.H0Cm - startS/10
		sr.HEndCm = run.H0Cm - endS/10
		sr.EStart = run.E0 - coeff*startS
		sr.EEnd = run.E0 - coeff*endS
		sr.DrainCm = sr.HStartCm / float64(run.DrainFactor)
		if sr.DeltaSmm < 0 {
			sr.Diagnostic = "卸载回弹，压缩量为负属正常"
		}
		cfg := configs[st.Seq]
		if cfg.Method == "" {
			cfg = domain.StepConfig{Method: "sqrt", WindowStartMin: 0.5, WindowEndMin: 9}
		}
		fw := FitWindow{Method: cfg.Method, StartMin: cfg.WindowStartMin, EndMin: cfg.WindowEndMin}
		sr.Fit = fitStep(res.Points, st.Seq, fitSeg, fw)
		if sr.Fit != nil {
			if cfg.Method == "sqrt" && sr.Fit.T90Min > 0 {
				sr.CvCm2Min = 0.848 * sr.DrainCm * sr.DrainCm / sr.Fit.T90Min
			} else if cfg.Method == "log" && sr.Fit.T50Min > 0 {
				sr.CvCm2Min = 0.197 * sr.DrainCm * sr.DrainCm / sr.Fit.T50Min
			}
			sr.CvCm2Sec = sr.CvCm2Min / 60
		}
		res.StepResults = append(res.StepResults, sr)
	}

	assignStepTimes(&res)
	buildCurves(&res)
	res.Casa = ComputeCasagrande(res, chosenCurve)
	return res
}

// longestHealthySegment 返回该级非回退段中读数最多的段索引；
// 只有归零单点的段不会被选为拟合段。
func longestHealthySegment(points []Point, step int) int {
	count := map[int]int{}
	for _, p := range points {
		if p.Step != step || p.Rollback || p.Reset {
			continue
		}
		count[p.Segment]++
	}
	best, bestN := -1, -1
	for seg, n := range count {
		if n > bestN {
			best, bestN = seg, n
		}
	}
	if best < 0 {
		// 没有非归零非回退点时退化为首个非回退段
		for _, p := range points {
			if p.Step == step && !p.Rollback {
				return p.Segment
			}
		}
	}
	return best
}

// assignStepTimes 以序列顺序累加每级时钟跨度，给出可连续展示的时间坐标。
func assignStepTimes(res *Result) {
	stepEnd := map[int]float64{} // 每级用于连续展示的起点
	cursor := 0.0
	for _, sr := range res.StepResults {
		stepEnd[sr.Step] = cursor
		maxClock := 0.0
		for _, p := range res.Points {
			if p.Step == sr.Step && !p.Rollback && p.ClockMin > maxClock {
				maxClock = p.ClockMin
			}
		}
		cursor += maxClock
	}
	for i := range res.Points {
		p := &res.Points[i]
		if p.Rollback {
			p.StepTime = stepEnd[p.Step] + p.ClockMin
			continue
		}
		p.StepTime = stepEnd[p.Step] + p.ClockMin
	}
}

func buildCurves(res *Result) {
	coeff := voidCoeff(res.Run)
	res.Casa.Initial = CurvePoint{Step: 0, Pressure: 0, E: res.Run.E0, Phase: domain.PhaseInitial}
	for _, sr := range res.StepResults {
		cp := CurvePoint{
			Step: sr.Step, Pressure: sr.Pressure, Phase: sr.Phase,
			E: res.Run.E0 - coeff*sr.EndSmm, PathIndex: len(res.Casa.FirstLoad) + len(res.Casa.Unload) + len(res.Casa.Reload),
		}
		switch sr.Phase {
		case domain.PhaseLoad:
			res.Casa.FirstLoad = append(res.Casa.FirstLoad, cp)
		case domain.PhaseUnload:
			res.Casa.Unload = append(res.Casa.Unload, cp)
		case domain.PhaseReload:
			res.Casa.Reload = append(res.Casa.Reload, cp)
		}
	}
}
