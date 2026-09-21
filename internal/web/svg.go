package web

import (
	"fmt"
	"math"
	"strings"
)

// chart is a minimal server-side SVG plotter. All charts are rendered on
// the server so the page works without a charting library.
type chart struct {
	W, H                   float64
	PadL, PadR, PadT, PadB float64
	XMin, XMax, YMin, YMax float64
	InvertY                bool // geotechnical convention: e grows upward
	body                   strings.Builder
}

func newChart(w, h, xmin, xmax, ymin, ymax float64) *chart {
	return &chart{W: w, H: h, PadL: 64, PadR: 16, PadT: 16, PadB: 40,
		XMin: xmin, XMax: xmax, YMin: ymin, YMax: ymax}
}

func (c *chart) px(x float64) float64 {
	return c.PadL + (x-c.XMin)/(c.XMax-c.XMin)*(c.W-c.PadL-c.PadR)
}

func (c *chart) py(y float64) float64 {
	if c.InvertY {
		return c.PadT + (y-c.YMin)/(c.YMax-c.YMin)*(c.H-c.PadT-c.PadB)
	}
	return c.H - c.PadB - (y-c.YMin)/(c.YMax-c.YMin)*(c.H-c.PadT-c.PadB)
}

// Inverse maps pixel coordinates back to data coordinates (used by the
// click-to-pick curvature candidate script).
func (c *chart) Inverse(px, py float64) (x, y float64) {
	x = c.XMin + (px-c.PadL)/(c.W-c.PadL-c.PadR)*(c.XMax-c.XMin)
	if c.InvertY {
		y = c.YMin + (py-c.PadT)/(c.H-c.PadT-c.PadB)*(c.YMax-c.YMin)
	} else {
		y = c.YMin + (c.H-c.PadB-py)/(c.H-c.PadT-c.PadB)*(c.YMax-c.YMin)
	}
	return x, y
}

type pt struct{ x, y float64 }

func (c *chart) polyline(pts []pt, stroke string, width float64, dash string) {
	if len(pts) < 2 {
		return
	}
	var b strings.Builder
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", c.px(p.x), c.py(p.y))
	}
	d := ""
	if dash != "" {
		d = fmt.Sprintf(` stroke-dasharray="%s"`, dash)
	}
	fmt.Fprintf(&c.body, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%.1f"%s/>`,
		b.String(), stroke, width, d)
}

func (c *chart) circle(p pt, r float64, fill, stroke string) {
	fmt.Fprintf(&c.body, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" stroke="%s" stroke-width="1"/>`,
		c.px(p.x), c.py(p.y), r, fill, stroke)
}

func (c *chart) text(x, y float64, anchor, size, fill, s string) {
	fmt.Fprintf(&c.body, `<text x="%.1f" y="%.1f" text-anchor="%s" font-size="%s" fill="%s">%s</text>`,
		x, y, anchor, size, fill, esc(s))
}

func (c *chart) vline(x float64, stroke, dash string) {
	fmt.Fprintf(&c.body, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1" stroke-dasharray="%s"/>`,
		c.px(x), c.PadT, c.px(x), c.H-c.PadB, stroke, dash)
}

func (c *chart) axes(xLabel, yLabel string, xTicks, yTicks []float64, fmtX, fmtY func(float64) string) {
	l, r, t, b := c.PadL, c.W-c.PadR, c.PadT, c.H-c.PadB
	fmt.Fprintf(&c.body, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="none" stroke="#888"/>`,
		l, t, r-l, b-t)
	for _, x := range xTicks {
		px := c.px(x)
		fmt.Fprintf(&c.body, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#888"/>`,
			px, b, px, b+4)
		c.text(px, b+16, "middle", "11", "#555", fmtX(x))
	}
	for _, y := range yTicks {
		py := c.py(y)
		fmt.Fprintf(&c.body, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#888"/>`,
			l-4, py, l, py)
		c.text(l-8, py+4, "end", "11", "#555", fmtY(y))
	}
	c.text((l+r)/2, c.H-6, "middle", "12", "#333", xLabel)
	fmt.Fprintf(&c.body, `<text x="14" y="%.1f" text-anchor="middle" font-size="12" fill="#333" transform="rotate(-90 14 %.1f)">%s</text>`,
		(t+b)/2, (t+b)/2, esc(yLabel))
}

func (c *chart) String() string {
	return fmt.Sprintf(`<svg viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" xmlns="http://www.w3.org/2000/svg">%s</svg>`,
		c.W, c.H, c.W, c.H, c.body.String())
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// ticks returns n roughly evenly spaced tick values between lo and hi.
func ticks(lo, hi float64, n int) []float64 {
	if n < 2 || hi <= lo {
		return []float64{lo}
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = lo + float64(i)*(hi-lo)/float64(n-1)
	}
	return out
}

func fmt2(v float64) string { return fmt.Sprintf("%.2f", v) }
func fmt1(v float64) string { return fmt.Sprintf("%.1f", v) }
func fmt0(v float64) string { return fmt.Sprintf("%.0f", v) }
func fmtSci(v float64) string {
	if v == 0 {
		return "0"
	}
	return fmt.Sprintf("%.2g", v)
}

var _ = math.Inf // keep math import if unused elsewhere
