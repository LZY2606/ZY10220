package svg

import (
	"fmt"
	"html"
	"math"

	C "consolidation-console/internal/consolidation"
)

// TimeDisplacement 绘制时间-累计压缩图。
// 灰色为原始读数（归零处可见跳变），彩色为修正值；
// 回退点红色空心单独成段，不与前后用线连接。
func TimeDisplacement(r *C.Result, width, height float64) string {
	c := newCanvas(width, height)
	c.start("时间 - 位移（按真实序列；灰=原始读数，彩=修正累计压缩）")

	pts := r.Points
	if len(pts) == 0 {
		return c.end()
	}
	tLo, tHi := 0.0, pts[len(pts)-1].StepTime
	for _, p := range pts {
		if p.StepTime > tHi {
			tHi = p.StepTime
		}
	}
	sLo, sHi := 0.0, 0.0
	for _, p := range pts {
		if p.Settlement > sHi {
			sHi = p.Settlement
		}
		if g := -p.GaugeMm; g > sHi {
			sHi = g
		}
	}
	sHi *= 1.05
	c.axes(tLo, tHi, sLo, sHi, "序列时间 (min)", "累计压缩 (mm)",
		niceTicks(tLo, tHi, 8, false), niceTicks(sLo, sHi, 6, false))

	drawSegments(c, pts, tLo, tHi, sLo, sHi, true)
	drawSegments(c, pts, tLo, tHi, sLo, sHi, false)

	for _, p := range pts {
		x := c.X(p.StepTime, tLo, tHi)
		col := phaseColor(p.Phase)
		if p.Rollback {
			c.circle(x, c.Y(p.Settlement, sLo, sHi), 3.5, "#fff", colorRollback)
			c.b.WriteString(fmt.Sprintf(
				`<text x="%.1f" y="%.1f" fill="%s" font-size="10">回退#%d</text>`,
				x+5, c.Y(p.Settlement, sLo, sHi)-4, colorRollback, p.ReadingID))
			continue
		}
		if p.Reset {
			c.circle(x, c.Y(p.Settlement, sLo, sHi), 3.5, "#fff", colorReset)
			c.b.WriteString(fmt.Sprintf(
				`<text x="%.1f" y="%.1f" fill="%s" font-size="10">归零#%d</text>`,
				x+5, c.Y(p.Settlement, sLo, sHi)+3, colorReset, p.ReadingID))
			continue
		}
		c.circle(x, c.Y(p.Settlement, sLo, sHi), 2.2, col, col)
	}
	c.legend(map[string]string{
		"首次加载": colorLoad, "卸载": colorUnload, "再加载": colorReload,
		"位移计归零": colorReset, "时钟回退": colorRollback, "原始读数": colorRaw,
	})
	return c.end()
}

func drawSegments(c *canvas, pts []C.Point, tLo, tHi, sLo, sHi float64, raw bool) {
	type key struct{ step, seg int }
	groups := map[key][]C.Point{}
	var order []key
	for _, p := range pts {
		k := key{p.Step, p.Segment}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}
	for _, k := range order {
		gp := groups[k]
		if gp[0].Rollback {
			continue
		}
		// 原始读数线：归零单点段不画；归零后表读数从 0 起算，与前段断开。
		if raw && len(gp) == 1 && gp[0].Reset {
			continue
		}
		path := ""
		for i, p := range gp {
			yval := p.Settlement
			if raw {
				yval = -p.GaugeMm
			}
			x := c.X(p.StepTime, tLo, tHi)
			y := c.Y(yval, sLo, sHi)
			if i == 0 {
				path += fmt.Sprintf("M%.1f %.1f ", x, y)
			} else {
				path += fmt.Sprintf("L%.1f %.1f ", x, y)
			}
		}
		stroke := phaseColor(gp[0].Phase)
		width := 1.8
		if raw {
			stroke, width = colorRaw, 1.0
		}
		c.b.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="%.1f"`,
			path, stroke, width))
		if raw {
			c.b.WriteString(` stroke-dasharray="3 3"`)
		}
		c.b.WriteString("/>")
	}
}

// ELogP 绘制孔隙比-log10(压力)；零荷载初始状态单独标注；
// 卸载、再加载分别连线，绝不按压力排序混入首次加载。
func ELogP(r *C.Result, width, height float64) string {
	c := newCanvas(width, height)
	c.start("孔隙比 e - log10(p)（零荷载为初始状态，不进入对数轴）")

	cs := r.Casa
	xLo := math.Log10(10.0)
	xHi := math.Log10(1000.0)
	eLo, eHi := cs.Initial.E, cs.Initial.E
	all := append([]C.CurvePoint{}, cs.FirstLoad...)
	all = append(all, cs.Unload...)
	all = append(all, cs.Reload...)
	for _, q := range all {
		if q.E < eLo {
			eLo = q.E
		}
		if q.E > eHi {
			eHi = q.E
		}
	}
	pad := (eHi - eLo) * 0.12
	eLo -= pad
	eHi += pad * 0.4

	c.axes(xLo, xHi, eLo, eHi, "log10 压力 (kPa)", "孔隙比 e",
		niceTicks(xLo, xHi, 6, true), niceTicks(eLo, eHi, 7, false))

	plotCurve := func(qs []C.CurvePoint, color string, dash bool) {
		path := ""
		for qi, q := range qs {
			x := c.X(math.Log10(q.Pressure), xLo, xHi)
			y := c.Y(q.E, eLo, eHi)
			cmd := "L"
			if qi == 0 {
				cmd = "M"
			}
			path += fmt.Sprintf("%s%.1f %.1f ", cmd, x, y)
			c.circle(x, y, 3, color, color)
			c.b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="9" fill="%s">%g</text>`,
				x+4, y-5, color, q.Pressure))
		}
		c.b.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="2"`, path, color))
		if dash {
			c.b.WriteString(` stroke-dasharray="5 3"`)
		}
		c.b.WriteString("/>")
	}
	plotCurve(cs.FirstLoad, colorLoad, false)
	plotCurve(cs.Unload, colorUnload, true)
	plotCurve(cs.Reload, colorReload, true)

	ix := c.ml
	iy := c.Y(cs.Initial.E, eLo, eHi)
	c.circle(ix, iy, 4, colorInitial, colorInitial)
	c.b.WriteString(fmt.Sprintf(
		`<text x="%.1f" y="%.1f" text-anchor="end" fill="%s">初始 p=0 e=%.3f</text>`,
		ix-6, iy-6, colorInitial, cs.Initial.E))

	if len(cs.FirstLoad) >= 2 {
		y1 := cs.VirginSlope*xLo + cs.VirginInter
		y2 := cs.VirginSlope*xHi + cs.VirginInter
		c.b.WriteString(fmt.Sprintf(
			`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1.4" stroke-dasharray="2 4"/>`,
			c.X(xLo, xLo, xHi), c.Y(y1, eLo, eHi),
			c.X(xHi, xLo, xHi), c.Y(y2, eLo, eHi), colorVirgin))
	}
	for _, cand := range cs.Candidates {
		x := c.X(math.Log10(cand.Pressure), xLo, xHi)
		y := c.Y(cand.E, eLo, eHi)
		chosen := cand.Index == cs.ChosenIndex
		ring := colorVirgin
		if chosen {
			ring = "#000"
		}
		c.circle(x, y, 6, "none", ring)
		if cand.GeometryOK {
			px := c.X(math.Log10(cand.PcKPa), xLo, xHi)
			py := c.Y(cs.VirginSlope*math.Log10(cand.PcKPa)+cs.VirginInter, eLo, eHi)
			c.circle(px, py, 4, ring, ring)
			c.b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" font-size="9">pc≈%.0f</text>`,
				px+4, py+12, cand.PcKPa))
			c.b.WriteString(fmt.Sprintf(
				`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="0.8" opacity="0.6"/>`,
				x, y, px, py, ring))
		}
	}
	c.legend(map[string]string{
		"首次加载": colorLoad, "卸载": colorUnload, "再加载": colorReload,
		"主压缩线/候选": colorVirgin, "初始零荷载": colorInitial,
	})
	return c.end()
}

// LoadPath 以路径顺序为横轴画压力，直观验证卸载未被排序进首次加载。
func LoadPath(r *C.Result, width, height float64) string {
	c := newCanvas(width, height)
	c.start("荷载路径（按读数序列；平台归零与回退位置标记）")

	srs := r.StepResults
	if len(srs) == 0 {
		return c.end()
	}
	pHi := 0.0
	for _, s := range srs {
		if s.Pressure > pHi {
			pHi = s.Pressure
		}
	}
	pHi *= 1.1
	xLo, xHi := -0.5, float64(len(srs))-0.5
	c.axes(xLo, xHi, 0, pHi, "荷载级（路径顺序）", "压力 (kPa)",
		nil, niceTicks(0, pHi, 6, false))

	path := ""
	for i, s := range srs {
		x := c.X(float64(i), xLo, xHi)
		y := c.Y(s.Pressure, 0, pHi)
		if i == 0 {
			path += fmt.Sprintf("M%.1f %.1f ", x, y)
		} else {
			path += fmt.Sprintf("L%.1f %.1f ", x, y)
		}
		col := phaseColor(s.Phase)
		c.circle(x, y, 4, col, col)
		label := fmt.Sprintf("#%d %g", s.Step, s.Pressure)
		c.b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="9">%s</text>`,
			x, c.h-26, html.EscapeString(label)))
		for _, seg := range s.Segments {
			if seg.HasReset {
				c.b.WriteString(fmt.Sprintf(
					`<rect x="%.1f" y="%.1f" width="8" height="8" fill="%s"/>`,
					x+7, y-12, colorReset))
			}
			if seg.Rollback {
				c.circle(x-7, y-8, 4, "#fff", colorRollback)
			}
		}
	}
	c.b.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="#555" stroke-width="1.5"/>`, path))
	c.legend(map[string]string{
		"首次加载": colorLoad, "卸载": colorUnload, "再加载": colorReload,
		"归零平台": colorReset, "时钟回退": colorRollback,
	})
	return c.end()
}

func (c *canvas) circle(x, y, r float64, fill, stroke string) {
	c.b.WriteString(fmt.Sprintf(
		`<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" stroke="%s" stroke-width="1.2"/>`,
		x, y, r, fill, stroke))
}

func (c *canvas) legend(items map[string]string) {
	// 固定顺序竖排，放在图右上角，避免中文宽度估算误差导致重叠。
	order := []string{"首次加载", "卸载", "再加载", "位移计归零", "时钟回退",
		"原始读数", "归零平台", "主压缩线/候选", "初始零荷载"}
	x := c.ml + c.pw() - 118
	y := c.mt + 4
	for _, label := range order {
		col, ok := items[label]
		if !ok {
			continue
		}
		c.b.WriteString(fmt.Sprintf(
			`<rect x="%.1f" y="%.1f" width="9" height="9" fill="%s"/><text x="%.1f" y="%.1f" font-size="10">%s</text>`,
			x, y, col, x+12, y+9, html.EscapeString(label)))
		y += 14
	}
}
