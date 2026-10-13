package dovi

import (
	"context"
	"fmt"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/vector"
	"image"
	"image/color"
	"image/draw"
	"math"
)

// PlotOptions uses inclusive original timeline indices; nil End differs from
// an explicit zero. The displayed crop begins at frame zero, like dovi_tool.
// MaxBytes bounds rendering allocations, excluding input and caller PNG encoding.
// Zero chooses 48 MiB and finite input limits.
type PlotOptions struct {
	Title      string
	Start, End *uint64
	MaxBytes   int64
	MaxRecords int
}

// Render returns an opaque 3000×1200 dovi_tool-style L1 plot in PQ coordinates.
// Missing samples remain gaps rather than synthetic brightness measurements.
func Render(ctx context.Context, a *Analysis, o PlotOptions) (image.Image, error) {
	if ctx == nil || o.MaxBytes < 0 || o.MaxRecords < 0 || len(o.Title) > 1024 {
		return nil, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.Validate(AnalysisOptions{MaxRecords: o.MaxRecords}); err != nil {
		return nil, err
	}
	budget := o.MaxBytes
	if budget == 0 {
		budget = 48 << 20
	}
	// Canvas, both vector buffers, fonts/glyphs and fixed caption inventories.
	if budget < 48<<20 {
		return nil, ErrLimit
	}
	start, end := uint64(0), uint64(a.length()-1)
	if o.Start != nil {
		start = *o.Start
	}
	if o.End != nil {
		end = *o.End
	}
	if start > end || end >= uint64(a.length()) {
		return nil, ErrInvalidOptions
	}
	data, err := collectPlot(ctx, a, int(start), int(end), budget-(40<<20))
	if err != nil {
		return nil, err
	}
	return renderPlot(ctx, a, int(start), int(end), o.Title, data)
}

func renderPlot(ctx context.Context, a *Analysis, start, end int, title string, data plotData) (image.Image, error) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	titleFace, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 40, DPI: 72})
	if err != nil {
		return nil, err
	}
	defer titleFace.Close()
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 24, DPI: 72})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	ticks, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 22, DPI: 72})
	if err != nil {
		return nil, err
	}
	defer ticks.Close()
	img := image.NewRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	if title == "" {
		title = "Dolby Vision L1 Dynamic Brightness"
	}
	title = fitText(titleFace, title, canvasWidth-120)
	text(img, titleFace, title, (canvasWidth-font.MeasureString(titleFace, title).Ceil())/2, 64)
	for i, caption := range data.leftCaptions {
		right := fitText(face, data.rightCaptions[i], chartRect.Dx()/2)
		width := chartRect.Dx()
		if right != "" {
			width -= font.MeasureString(face, right).Ceil() + 30
		}
		text(img, face, fitText(face, caption, width), chartRect.Min.X, 96+i*25)
		text(img, face, right, chartRect.Max.X-font.MeasureString(face, right).Ceil(), 96+i*25)
	}
	ceiling := data.ceiling
	// Upstream has a degenerate zero range for black metadata; use a finite axis.
	if ceiling == 0 {
		ceiling = 1.0 / 4095
	}
	drawAxes(img, face, ticks, end-start+1, ceiling)
	raster := vector.NewRasterizer(chartRect.Dx(), chartRect.Dy())
	for series := range 3 {
		if err := drawSeries(ctx, img, raster, a, start, end, series, ceiling); err != nil {
			return nil, err
		}
	}
	plotFrame(img)
	drawLegend(img, face, data.labels())
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return img, nil
}

func drawAxes(img *image.RGBA, face, tickFace font.Face, count int, ceiling float64) {
	peakNits, _ := PQToNits(ceiling)
	roundedPeak, _ := NitsToPQ(math.Round(peakNits))
	for _, pq := range plotTicks(ceiling) {
		if pq < 0 || pq > max(ceiling, roundedPeak) {
			continue
		}
		y := chartRect.Max.Y - int(math.Round(min(pq/ceiling, 1)*float64(chartRect.Dy())))
		line(img, image.Rect(chartRect.Min.X, y, chartRect.Max.X, y+1).Intersect(chartRect), color.NRGBA{A: 26})
		n, _ := PQToNits(pq)
		label := fmt.Sprintf("%g", math.Round(n*1000)/1000)
		text(img, tickFace, label, chartRect.Min.X-8-font.MeasureString(tickFace, label).Ceil(), y+7)
	}
	for tick := 0; tick <= count; tick += tickInterval(count, 24) {
		x := chartRect.Min.X + int(float64(tick)/float64(count)*float64(chartRect.Dx()))
		line(img, image.Rect(x, chartRect.Min.Y, x+1, chartRect.Max.Y).Intersect(chartRect), color.NRGBA{A: 26})
		label := fmt.Sprint(tick)
		text(img, tickFace, label, x-font.MeasureString(tickFace, label).Ceil()/2, chartRect.Max.Y+28)
	}
	label := "frames"
	text(img, face, label, chartRect.Min.X+(chartRect.Dx()-font.MeasureString(face, label).Ceil())/2, 1164)
	label = "nits (cd/m²)"
	rot := image.NewRGBA(image.Rect(0, 0, font.MeasureString(face, label).Ceil()+4, 36))
	text(rot, face, label, 2, 26)
	top := chartRect.Min.Y + (chartRect.Dy()-rot.Bounds().Dx())/2
	for y := range rot.Bounds().Dy() {
		for x := range rot.Bounds().Dx() {
			img.SetRGBA(62+y, top+rot.Bounds().Dx()-1-x, compositeWhite(rot.RGBAAt(x, y)))
		}
	}
}

func drawSeries(ctx context.Context, img *image.RGBA, raster *vector.Rasterizer, a *Analysis, start, end, series int, ceiling float64) error {
	w, h := chartRect.Dx(), chartRect.Dy()
	// Iterate the retained samples directly: no downsampling or per-frame copy.
	for pass := range 2 {
		raster.Reset(w, h)
		raster.DrawOp = draw.Over
		run := false
		var px, py float64
		runLength := 0
		closeRun := func() {
			if run && pass == 0 {
				raster.LineTo(float32(px), float32(h))
				raster.ClosePath()
			}
			run = false
		}
		for i := start; i <= end+1; i++ {
			if i%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			var v *L1
			if i <= end {
				if r := a.record(i); r != nil && r.DM != nil {
					v = r.DM.L1
				}
			}
			if v == nil {
				closeRun()
				continue
			}
			value := [3]uint16{v.MaxPQ, v.AvgPQ, v.MinPQ}[series]
			x := float64(i-start) / float64(end-start+1) * float64(w)
			y := (1 - float64(value)/4095/ceiling) * float64(h)
			if !run {
				runLength = 0
				if pass == 0 {
					raster.MoveTo(float32(x), float32(h))
					raster.LineTo(float32(x), float32(max(0, y)))
				}
			} else if pass == 0 {
				// Clip before rasterization: offscreen rows still cost work in vector.
				// Keep the intersection so clipping does not change visible slopes.
				if (py < 0) != (y < 0) {
					crossing := px + (x-px)*(-py)/(y-py)
					raster.LineTo(float32(crossing), 0)
				}
				raster.LineTo(float32(x), float32(max(0, y)))
			} else {
				// Clip above the max-series range without moving crossing slopes.
				x0, y0, x1, y1 := px, py, x, y
				if y0 >= 0 || y1 >= 0 {
					if y0 < 0 {
						x0 += (x1 - x0) * (-y0) / (y1 - y0)
						y0 = 0
					} else if y1 < 0 {
						x1 = x0 + (x1-x0)*(-y0)/(y1-y0)
						y1 = 0
					}
					stroke(raster, x0, y0, x1, y1)
				}
			}
			runLength++
			run = true
			px, py = x, y
			if pass == 1 && runLength == 1 {
				// Keep isolated retained samples visible, without bridging gaps.
				nextMissing := i == end
				if !nextMissing {
					r := a.record(i + 1)
					nextMissing = r == nil || r.DM == nil || r.DM.L1 == nil
				}
				if nextMissing && y >= 0 {
					// Keep the marker inside the viewport so the axis cannot erase it.
					mx, my := max(2, x), max(2, min(float64(h)-2, y))
					raster.MoveTo(float32(mx-2), float32(my-2))
					raster.LineTo(float32(mx+2), float32(my-2))
					raster.LineTo(float32(mx+2), float32(my+2))
					raster.LineTo(float32(mx-2), float32(my+2))
					raster.ClosePath()
				}
			}
		}
		c := plotColors[series]
		if pass == 0 {
			c.A = [3]uint8{64, 128, 128}[series]
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		raster.Draw(img, chartRect, image.NewUniform(c), image.Point{})
	}
	return nil
}

func drawLegend(img *image.RGBA, face font.Face, labels [3]string) {
	width := 0
	for _, label := range labels {
		width = max(width, font.MeasureString(face, label).Ceil())
	}
	width += 64
	x, bottom := chartRect.Min.X+12, chartRect.Max.Y-12
	box := image.Rect(x, bottom-116, x+width, bottom)
	draw.Draw(img, box, image.White, image.Point{}, draw.Src)
	for _, r := range []image.Rectangle{image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+1), image.Rect(box.Min.X, box.Max.Y-1, box.Max.X, box.Max.Y), image.Rect(box.Min.X, box.Min.Y, box.Min.X+1, box.Max.Y), image.Rect(box.Max.X-1, box.Min.Y, box.Max.X, box.Max.Y)} {
		line(img, r, color.Black)
	}
	for i, label := range labels {
		y := box.Min.Y + 28 + i*34
		line(img, image.Rect(x+12, y-9, x+32, y-7), plotColors[i])
		text(img, face, label, x+44, y)
	}
}
