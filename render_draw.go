package dovi

import (
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"unicode"
)

const canvasWidth, canvasHeight = 3000, 1200

// Match the upstream chart layout with room for the embedded Go font's y label.
var chartRect = image.Rect(140, 162, 2940, 1110)
var plotColors = [3]color.NRGBA{{R: 65, G: 105, B: 225, A: 255}, {R: 75, B: 130, A: 255}, {A: 255}}

func text(dst draw.Image, face font.Face, value string, x, baseline int) {
	drawer := font.Drawer{Dst: dst, Src: image.Black, Face: face, Dot: fixed.P(x, baseline)}
	drawer.DrawString(value)
}

// fitText sanitizes display text only, keeping the stored strings unchanged.
func fitText(face font.Face, value string, width int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		if _, ok := face.GlyphAdvance(r); !ok {
			if _, ok := face.GlyphAdvance('\ufffd'); ok {
				return '\ufffd'
			}
			return '?'
		}
		return r
	}, value)
	if font.MeasureString(face, value).Ceil() <= width {
		return value
	}
	const ellipsis = "\u2026"
	limit := fixed.I(width) - font.MeasureString(face, ellipsis)
	var advance fixed.Int26_6
	var previous rune
	for i, r := range value {
		if i > 0 {
			advance += face.Kern(previous, r)
		}
		a, _ := face.GlyphAdvance(r)
		if advance+a > limit {
			return value[:i] + ellipsis
		}
		advance += a
		previous = r
	}
	return value
}

func line(img *image.RGBA, rect image.Rectangle, value color.Color) {
	draw.Draw(img, rect, image.NewUniform(value), image.Point{}, draw.Over)
}

func compositeWhite(c color.RGBA) color.RGBA {
	return color.RGBA{R: c.R + 255 - c.A, G: c.G + 255 - c.A, B: c.B + 255 - c.A, A: 255}
}

func plotFrame(img *image.RGBA) {
	r := chartRect
	line(img, image.Rect(r.Min.X, r.Max.Y, r.Max.X+1, r.Max.Y+1), color.Black)
	line(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y+1), color.Black)
}

func stroke(raster *vector.Rasterizer, x0, y0, x1, y1 float64) {
	length := math.Hypot(x1-x0, y1-y0)
	if length == 0 {
		return
	}
	dx, dy := (y1-y0)/(2*length), (x0-x1)/(2*length)
	raster.MoveTo(float32(x0+dx), float32(y0+dy))
	raster.LineTo(float32(x1+dx), float32(y1+dy))
	raster.LineTo(float32(x1-dx), float32(y1-dy))
	raster.LineTo(float32(x0-dx), float32(y0-dy))
	raster.ClosePath()
}
