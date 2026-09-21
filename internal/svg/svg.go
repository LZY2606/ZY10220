// Package svg 用标准库直接生成查验台需要的三张图：
// 时间-位移（原始/修正、回退分段不拼接）、e-log(p)（含 Casagrande 几何）、
// 荷载路径（按真实序列，卸载/再加载独立着色）。
package svg

import (
	"fmt"
	"html"
	"math"
	"strings"
)

const (
	colorLoad     = "#1f6feb"
	colorUnload   = "#d1242f"
	colorReload   = "#1a7f37"
	colorInitial  = "#8c8c8c"
	colorRaw      = "#bfbfbf"
	colorReset    = "#bc4c00"
	colorRollback = "#cf222e"
	colorFit      = "#000000"
	colorVirgin   = "#8250df"
)

type canvas struct {
	w, h, ml, mr, mt, mb float64
	b                    strings.Builder
}

func newCanvas(w, h float64) *canvas {
	return &canvas{w: w, h: h, ml: 58, mr: 18, mt: 18, mb: 42}
}

func (c *canvas) pw() float64 { return c.w - c.ml - c.mr }
func (c *canvas) ph() float64 { return c.h - c.mt - c.mb }
func (c *canvas) X(v, lo, hi float64) float64 {
	return c.ml + (v-lo)/(hi-lo)*c.pw()
}
func (c *canvas) Y(v, lo, hi float64) float64 {
	return c.mt + (hi-v)/(hi-lo)*c.ph()
}

func (c *canvas) start(title string) {
	c.b.WriteString(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" font-family="ui-sans-serif,system-ui" font-size="11">`,
		c.w, c.h))
	c.b.WriteString(fmt.Sprintf(`<rect width="%.0f" height="%.0f" fill="#fff"/>`, c.w, c.h))
	c.b.WriteString(fmt.Sprintf(`<text x="%.0f" y="14" font-weight="700">%s</text>`, c.ml, html.EscapeString(title)))
}
func (c *canvas) end() string { c.b.WriteString("</svg>"); return c.b.String() }

func (c *canvas) axes(xLo, xHi, yLo, yHi float64, xLabel, yLabel string, xTicks, yTicks []tick) {
	c.b.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#333"/>`,
		c.ml, c.mt, c.ml, c.mt+c.ph()))
	c.b.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#333"/>`,
		c.ml, c.mt+c.ph(), c.ml+c.pw(), c.mt+c.ph()))
	for _, t := range xTicks {
		x := c.X(t.v, xLo, xHi)
		c.b.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#e5e5e5"/>`,
			x, c.mt, x, c.mt+c.ph()))
		c.b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`,
			x, c.mt+c.ph()+15, html.EscapeString(t.label)))
	}
	for _, t := range yTicks {
		y := c.Y(t.v, yLo, yHi)
		c.b.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#f0f0f0"/>`,
			c.ml, y, c.ml+c.pw(), y))
		c.b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="end" dominant-baseline="middle">%s</text>`,
			c.ml-5, y, html.EscapeString(t.label)))
	}
	c.b.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`,
		c.ml+c.pw()/2, c.h-6, html.EscapeString(xLabel)))
	c.b.WriteString(fmt.Sprintf(
		`<text transform="translate(15 %.1f) rotate(-90)" text-anchor="middle">%s</text>`,
		c.mt+c.ph()/2, html.EscapeString(yLabel)))
}

type tick struct {
	v     float64
	label string
}

func niceTicks(lo, hi float64, n int, log10scale bool) []tick {
	if log10scale {
		var ts []tick
		start := math.Floor(lo)
		for x := start; x <= hi+1e-9; x++ {
			ts = append(ts, tick{x, fmt.Sprintf("%.0f", math.Pow(10, x))})
		}
		return ts
	}
	var ts []tick
	step := (hi - lo) / float64(n)
	if step <= 0 {
		step = 1
	}
	step = niceStep(step)
	start := math.Ceil(lo/step) * step
	for v := start; v <= hi+step*1e-6; v += step {
		ts = append(ts, tick{v, fmt.Sprintf("%g", sig3(v))})
	}
	return ts
}

func niceStep(raw float64) float64 {
	pow := math.Pow(10, math.Floor(math.Log10(raw)))
	frac := raw / pow
	switch {
	case frac <= 1:
		return pow
	case frac <= 2:
		return 2 * pow
	case frac <= 5:
		return 5 * pow
	default:
		return 10 * pow
	}
}

func sig3(v float64) float64 {
	if v == 0 {
		return 0
	}
	p := math.Pow(10, math.Floor(math.Log10(math.Abs(v))))
	return math.Round(v/p*100) / 100 * p
}

func phaseColor(phase string) string {
	switch phase {
	case "load":
		return colorLoad
	case "unload":
		return colorUnload
	case "reload":
		return colorReload
	default:
		return colorInitial
	}
}
