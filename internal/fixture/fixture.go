// Package fixture 生成固定的固结试验样本。
// 所有数值由确定公式产生，不依赖随机源，保证清空后重放一致。
package fixture

import (
	"math"

	"consolidation-console/internal/domain"
)

const (
	H0Cm        = 2.0
	E0          = 2.80
	AreaCm2     = 30.0
	DrainFactor = 2 // 双面排水
)

// 各级末累计压缩量(mm)：先按荷载路径顺序定义，
// 卸载/再加载不得按压力排序，否则会被混入首次加载曲线。
var spec = []struct {
	pressure float64
	phase    string
	endSmm   float64
	t90min   float64
}{
	{25, domain.PhaseLoad, 0.20, 45},
	{50, domain.PhaseLoad, 0.37, 42},
	{100, domain.PhaseLoad, 0.72, 40}, // 本级注入时钟回退
	{200, domain.PhaseLoad, 1.55, 38},
	{400, domain.PhaseLoad, 2.785, 35}, // 平台上位移计归零
	{800, domain.PhaseLoad, 3.917, 33},
	{400, domain.PhaseUnload, 3.50, 40},
	{200, domain.PhaseUnload, 3.20, 42},
	{100, domain.PhaseUnload, 2.85, 44},
	{50, domain.PhaseUnload, 2.63, 48},
	{100, domain.PhaseReload, 2.69, 42},
	{200, domain.PhaseReload, 2.95, 39},
	{400, domain.PhaseReload, 3.38, 36},
	{800, domain.PhaseReload, 3.85, 34},
}

var clockGrid = []float64{0, 0.5, 1, 2, 4, 9, 16, 25, 49, 64, 100, 144}

// U 反演自 Terzaghi 一维固结余弦解（Tv90=0.848）。
func degree(Tv float64) float64 {
	sum := 0.0
	for k := 0; k < 32; k++ {
		mv := float64(2*k + 1)
		sum += 1.0 / (mv * mv) * math.Exp(-math.Pi*math.Pi*mv*mv*Tv/4.0)
	}
	return 1.0 - (8.0/(math.Pi*math.Pi))*sum
}

// Build 返回固定 fixture：运行头、级次表、原始读数、默认拟合配置。
func Build() (domain.Run, []domain.StepSpec, []domain.Reading, []domain.StepConfig) {
	run := domain.Run{
		Name:        "固定样本：先加载-卸载-再加载（含归零与时钟回退）",
		H0Cm:        H0Cm,
		E0:          E0,
		AreaCm2:     AreaCm2,
		DrainFactor: DrainFactor,
	}
	steps := make([]domain.StepSpec, len(spec))
	cfgs := make([]domain.StepConfig, len(spec))
	readings := []domain.Reading{}
	var id int64 = 1
	prevEnd := 0.0
	prevEndGauge := 0.0 // 上一级末表读数（压缩为负）
	for i, s := range spec {
		seq := i + 1
		steps[i] = domain.StepSpec{Seq: seq, PressureKPa: s.pressure, Phase: s.phase}
		cfgs[i] = domain.StepConfig{Step: seq, Method: "sqrt", WindowStartMin: 0.5, WindowEndMin: 9}
		delta := s.endSmm - prevEnd
		// 常规级平台连续：本级起点表读数=上一级最后一条读数。
		// 级末不完全固结(uLast)时，表读数尚未到 -endSmm，下一级在此基础上继续记录。
		uLast := degree(0.848 * clockGrid[len(clockGrid)-1] / s.t90min)
		endGaugeDelta := -delta * uLast
		for j, t := range clockGrid {
			u := degree(0.848 * t / s.t90min)
			increment := delta * u // 本级新增压缩随固结度增长
			note := ""
			reset := false
			var gauge float64
			if seq == 5 {
				// 第 5 级(400kPa)平台：加载瞬间仪器归零，归零后读数从 0 重新记起。
				reset = j == 0
				gauge = -increment
				if reset {
					gauge = 0
					note = "平台位移计归零"
				}
			} else {
				gauge = prevEndGauge - increment
			}
			readings = append(readings, domain.Reading{
				ID:          id,
				Seq:         seq,
				OrigStep:    seq,
				PressureKPa: s.pressure,
				Phase:       s.phase,
				ClockMin:    t,
				GaugeMm:     math.Round(gauge*1000) / 1000,
				Reset:       reset,
				Note:        note,
			})
			id++
		}
		// 第 3 级(100kPa)：中途时钟回退，插入一个时间戳倒退的孤立读数。
		if seq == 3 {
			tBad := 7.0
			u := degree(0.848 * tBad / s.t90min)
			gauge := prevEndGauge - delta*u
			readings = append(readings, domain.Reading{
				ID:          id,
				Seq:         seq,
				OrigStep:    seq,
				PressureKPa: s.pressure,
				Phase:       s.phase,
				ClockMin:    tBad,
				GaugeMm:     math.Round(gauge*1000) / 1000,
				Note:        "时钟回退读数",
			})
			id++
		}
		prevEndGauge += endGaugeDelta
		prevEnd += delta * uLast // 本级实际末累计压缩（级末未完全固结）
	}
	return run, steps, readings, cfgs
}
