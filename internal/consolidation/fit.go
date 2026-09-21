package consolidation

import "math"

// fitStep 只使用指定健康分段（排除回退段），在人工选择拟合窗内做线性回归。
// 纵轴统一为本级相对压缩（相对本级起点物理累计的增量，mm；回弹时为负）。
func fitStep(points []Point, step, segIdx int, w FitWindow) *FitGeometry {
	type tp struct{ t, s float64 }

	base := earliestBase(points, step, segIdx)
	var data []tp
	for _, p := range points {
		if p.Step != step || p.Rollback {
			continue
		}
		if segIdx >= 0 && p.Segment != segIdx {
			continue
		}
		if t := p.ClockMin; t >= w.StartMin && t <= w.EndMin && t > 0 {
			data = append(data, tp{t, p.Settlement - base})
		}
	}
	if len(data) < 2 {
		return &FitGeometry{Method: w.Method, Window: w,
			Note: "拟合窗内有效读数不足（可能落在回退段），无法计算固结系数"}
	}

	var xs, ys []float64
	for _, d := range data {
		if w.Method == "sqrt" {
			xs = append(xs, math.Sqrt(d.t))
		} else {
			xs = append(xs, math.Log10(d.t))
		}
		ys = append(ys, d.s)
	}
	m, b, r2 := linreg(xs, ys)
	g := &FitGeometry{Method: w.Method, Window: w, R2: r2}
	g.X1, g.X2 = xs[0], xs[len(xs)-1]
	g.Y1 = m*g.X1 + b
	g.Y2 = m*g.X2 + b

	if w.Method == "sqrt" {
		// Taylor 根号时间法（Tv90=0.848）：
		// 早期 d-√t 直线经原点，斜率 m（本级压缩/√min）。
		// 以 d0 为零点、d100 为完全主固结沉降：直线 d=d0+m√t 到达 d100 时为 √t100，
		// d90=d0+0.9(d100-d0) 对应 √t90=0.9√t100。
		// 经典作图中 √t90 是“原点射线”与“d90 水平线”交点横坐标的 1.15 倍关系，
		// 这里直接用等价代数构造，避免人工读图误差。
		d0 := b
		g.D0 = d0
		if m <= 1e-12 {
			g.Note = "根号时间法：主段斜率非正（回弹或窗内无线性段），无法确定 t90"
			return g
		}
		// d100 取本级健康分段最大实测相对压缩（末读数），不局限于拟合窗。
		d100 := stepRelativeMax(points, step, segIdx)
		g.D100 = d100
		if d100 <= d0 {
			g.Note = "根号时间法：d100 不大于 d0，无法确定 t90"
			return g
		}
		sqrtT100 := (d100 - d0) / m
		sqrtT90 := 1.15 * sqrtT100
		g.D90 = d0 + m*sqrtT90
		g.T90Min = sqrtT90 * sqrtT90
		g.Note = "根号时间法：t90=(1.15·√t100)²，d100 取本级末实测"
		return g
	}

	// Casagrande 对数时间法（Tv50=0.197）：
	// d0=2·d(0.25)-d(1)；d100 为主固结线与次固结线切线交点；d50=(d0+d100)/2。
	d025 := valueAt(points, step, segIdx, base, 0.25)
	d1 := valueAt(points, step, segIdx, base, 1.0)
	d0 := 2*d025 - d1
	g.D0 = d0
	if len(data) >= 3 {
		var lateX, lateY []float64
		for _, d := range data[len(data)-3:] {
			lateX = append(lateX, math.Log10(d.t))
			lateY = append(lateY, d.s)
		}
		m2, b2, _ := linreg(lateX, lateY)
		if math.Abs(m-m2) > 1e-12 {
			xi := (b2 - b) / (m - m2)
			d100 := m*xi + b
			g.D100 = d100
			d50 := (d0 + d100) / 2
			g.D50 = d50
			if (d50-d0)*(d100-d0) > 0 && math.Abs(d50-d0) < math.Abs(d100-d0) {
				xt := (d50 - b) / m
				if xt > 0 {
					g.T50Min = math.Pow(10, xt)
				}
			}
		}
	}
	g.Note = "对数时间法：d0=2d(0.25)-d(1)；d100 为主/次固结切线交点；d50 为中点"
	return g
}

// earliestBase 返回健康分段内最早读数的物理累计压缩（本级基准）。
func earliestBase(points []Point, step, segIdx int) float64 {
	bestClock := 1e18
	base := 0.0
	for _, p := range points {
		if p.Step != step || p.Rollback {
			continue
		}
		if segIdx >= 0 && p.Segment != segIdx {
			continue
		}
		if p.ClockMin < bestClock {
			bestClock = p.ClockMin
			base = p.Settlement
		}
	}
	return base
}

// valueAt 取最接近 target 时钟读数的本级相对压缩；不存在精确点时线性插值。
func valueAt(points []Point, step, segIdx int, base, target float64) float64 {
	type pair struct{ t, s float64 }
	var ps []pair
	for _, p := range points {
		if p.Step != step || p.Rollback {
			continue
		}
		if segIdx >= 0 && p.Segment != segIdx {
			continue
		}
		ps = append(ps, pair{p.ClockMin, p.Settlement - base})
	}
	if len(ps) == 0 {
		return 0
	}
	for i := 0; i < len(ps)-1; i++ {
		if (ps[i].t <= target && ps[i+1].t >= target) || (ps[i].t >= target && ps[i+1].t <= target) {
			if ps[i+1].t == ps[i].t {
				return ps[i].s
			}
			f := (target - ps[i].t) / (ps[i+1].t - ps[i].t)
			return ps[i].s + f*(ps[i+1].s-ps[i].s)
		}
	}
	if math.Abs(ps[0].t-target) < math.Abs(ps[len(ps)-1].t-target) {
		return ps[0].s
	}
	return ps[len(ps)-1].s
}

func linreg(xs, ys []float64) (m, b, r2 float64) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy, syy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
		syy += ys[i] * ys[i]
	}
	den := n*sxx - sx*sx
	if math.Abs(den) < 1e-15 {
		return 0, sy / n, 0
	}
	m = (n*sxy - sx*sy) / den
	b = (sy - m*sx) / n
	num := n*sxy - sx*sy
	if den2 := den * (n*syy - sy*sy); den2 > 0 {
		r2 = num * num / den2
	}
	return m, b, r2
}

// stepRelativeMax 返回健康分段内最大本级相对压缩（回弹时为最小，取绝对值更大者）。
func stepRelativeMax(points []Point, step, segIdx int) float64 {
	base := earliestBase(points, step, segIdx)
	maxPos, minNeg := 0.0, 0.0
	for _, p := range points {
		if p.Step != step || p.Rollback {
			continue
		}
		if segIdx >= 0 && p.Segment != segIdx {
			continue
		}
		v := p.Settlement - base
		if v > maxPos {
			maxPos = v
		}
		if v < minNeg {
			minNeg = v
		}
	}
	if -minNeg > maxPos {
		return minNeg
	}
	return maxPos
}
