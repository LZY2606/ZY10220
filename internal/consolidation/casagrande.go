package consolidation

import (
	"math"

	"consolidation-console/internal/domain"
)

// ComputeCasagrande 只在首次加载曲线（不含零荷载初始点）上寻找曲率点。
// 卸载/再加载点严格保留为独立路径，不参与排序、不混入首次曲线。
func ComputeCasagrande(res Result, chosenIndex int) CasagrandeResult {
	c := res.Casa
	fl := c.FirstLoad
	// x=log10(p)，压力必须为正。
	type xy struct{ x, y, p, e float64 }
	pts := make([]xy, 0, len(fl))
	for _, q := range fl {
		if q.Pressure > 0 {
			pts = append(pts, xy{math.Log10(q.Pressure), q.E, q.Pressure, q.E})
		}
	}

	// 主压缩（Virgin）线：找相邻斜率最陡的内部位置，
	// 用其右侧所有点（含该点）回归；点数不足 2 时退化为末段 3 点。
	knee := 0
	steep := 0.0
	for i := 1; i < len(pts); i++ {
		sl := (pts[i].y - pts[i-1].y) / (pts[i].x - pts[i-1].x)
		if sl < steep {
			steep, knee = sl, i
		}
	}
	seg := pts[knee:]
	if len(seg) < 2 {
		n := 3
		if len(pts) < n {
			n = len(pts)
		}
		seg = pts[len(pts)-n:]
	}
	var vx, vy []float64
	for _, q := range seg {
		vx = append(vx, q.x)
		vy = append(vy, q.y)
	}
	vm, vb, _ := linreg(vx, vy)
	c.VirginSlope, c.VirginInter = vm, vb

	type turnInfo struct {
		i       int
		turnDeg float64
		curv    float64
		local   bool
	}
	var turns []turnInfo
	for i := 1; i < len(pts)-1; i++ {
		a1 := math.Atan2(pts[i].y-pts[i-1].y, pts[i].x-pts[i-1].x)
		a2 := math.Atan2(pts[i+1].y-pts[i].y, pts[i+1].x-pts[i].x)
		tn := math.Abs(a2 - a1)
		if tn > math.Pi {
			tn = 2*math.Pi - tn
		}
		// Menger 圆曲率（相邻三点）。
		curv := menger(pts[i-1].x, pts[i-1].y, pts[i].x, pts[i].y, pts[i+1].x, pts[i+1].y)
		turns = append(turns, turnInfo{i, tn * 180 / math.Pi, curv, false})
	}
	if len(turns) == 0 {
		c.Note = "首次加载点数不足，无法构造 Casagrande 候选"
		return c
	}
	maxTurn := 0.0
	for _, t := range turns {
		if t.turnDeg > maxTurn {
			maxTurn = t.turnDeg
		}
	}
	for k := range turns {
		i := turns[k].i
		local := true
		if k > 0 {
			local = local && turns[k].turnDeg >= turns[k-1].turnDeg
		}
		if k < len(turns)-1 {
			local = local && turns[k].turnDeg >= turns[k+1].turnDeg
		}
		turns[k].local = local
		// 所有内部点都保留为曲率点候选，供人工判断；
		// 弱曲率点几何上无意义（在光滑段角平分线交点不应采信），
		// 但其 pc 仍照算并在页面标记，不进入先期压力范围。
		q := pts[i]
		cand := Candidate{
			Index: i, Step: fl[i].Step, Pressure: q.p, E: q.e,
			Curvature: turns[k].curv, TurnDeg: turns[k].turnDeg,
			LocalMaximum: local,
		}
		// Casagrande：候选点作水平切线，与主压缩线夹角的角平分线；
		// 角平分线与主压缩线交点横坐标即 log10(pc)。
		theta := math.Atan(vm) // 主压缩线相对水平轴角（负值）
		phi := theta / 2       // 水平切线(0)与主压缩线的角平分线角
		mb := math.Tan(phi)    // 角平分线斜率
		den := mb - vm
		if math.Abs(den) > 1e-9 {
			// qy+mb(x-qx)=vm*x+vb → x=(vb-qy+mb*qx)/(mb-vm)
			xc := (vb - q.y + mb*q.x) / den
			if xc > q.x { // 交点必须落在候选点右侧（更大压力）
				pc := math.Pow(10, xc)
				cand.PcKPa = pc
				cand.GeometryOK = true
			}
		}
		c.Candidates = append(c.Candidates, cand)
	}

	threshold := math.Max(3.0, maxTurn/4.0)
	valid := []Candidate{}
	for _, cand := range c.Candidates {
		if cand.GeometryOK && cand.TurnDeg >= threshold {
			valid = append(valid, cand)
		}
	}
	if len(valid) == 0 {
		c.Note = "没有候选能在其右侧与主压缩线相交，请检查曲线"
		return c
	}
	low, high := valid[0].PcKPa, valid[0].PcKPa
	auto := valid[0]
	for _, cand := range valid {
		if cand.PcKPa < low {
			low = cand.PcKPa
		}
		if cand.PcKPa > high {
			high = cand.PcKPa
		}
		// 自动选择：优先局部曲率最大，其次曲率最大。
		if (cand.LocalMaximum && !auto.LocalMaximum) ||
			(cand.LocalMaximum == auto.LocalMaximum && cand.Curvature > auto.Curvature) {
			auto = cand
		}
	}
	c.PcLow, c.PcHigh = low, high
	c.PcAuto = auto.PcKPa
	c.ChosenIndex = -1
	c.PcChosen = auto.PcKPa
	for _, cand := range valid {
		if cand.Index == chosenIndex {
			c.ChosenIndex = chosenIndex
			c.PcChosen = cand.PcKPa
		}
	}
	if low == high {
		c.Note = "唯一几何候选"
	} else {
		c.Note = "曲率点不唯一：先期固结压力以候选范围给出，可在页面人工选定"
	}
	return c
}

func menger(ax, ay, bx, by, cx, cy float64) float64 {
	area := math.Abs((bx-ax)*(cy-ay)-(by-ay)*(cx-ax)) / 2
	dab := math.Hypot(bx-ax, by-ay)
	dbc := math.Hypot(cx-bx, cy-by)
	dac := math.Hypot(cx-ax, cy-ay)
	den := dab * dbc * dac
	if den < 1e-15 {
		return 0
	}
	return 4 * area / den
}

var _ = domain.PhaseLoad
