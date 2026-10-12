package dovi

import (
	"context"
	"fmt"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"unicode/utf8"
)

// PlotOptions uses inclusive original timeline indices; nil End differs from
// an explicit zero. MaxBytes bounds this call's allocations, excluding input
// and caller PNG encoding. Zero chooses 32 MiB and finite input limits.
type PlotOptions struct {
	Title      string
	Start, End *uint64
	MaxBytes   int64
	MaxRecords int
}
type plotBucket struct {
	first, last, lo, hi [3]uint16
	present, gap        bool
}

// Render returns an opaque 3000×1200 image of L1 metadata in PQ coordinates.
// Missing samples never become zero brightness or connect adjacent runs.
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
		budget = 32 << 20
	}
	if budget < 24<<20 {
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
	const left, right, top, bottom = 190, 2880, 185, 990
	const width = right - left + 1
	buckets := make([]plotBucket, width)
	var stats statisticAccumulator
	var profiles [9]bool
	var cm29, cm40, scenes, l5, trims int
	var source [2]uint16
	var mastering L6
	var sourceCount, l6Count int
	var sourceMixed, l6Mixed bool
	for i := start; i <= end; i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		x := (width - 1) / 2
		if end > start {
			x = int((i - start) * uint64(width-1) / (end - start))
		}
		bucket := &buckets[x]
		r := a.record(int(i))
		if r == nil {
			bucket.gap = true
			continue
		}
		profiles[r.Profile] = true
		if r.DM == nil {
			bucket.gap = true
			continue
		}
		d := r.DM
		if d.Base != nil {
			value := [2]uint16{d.Base.SourceMinPQ, d.Base.SourceMaxPQ}
			if sourceCount == 0 {
				source = value
			} else if source != value {
				sourceMixed = true
			}
			sourceCount++
		}
		if d.L6 != nil {
			if l6Count == 0 {
				mastering = *d.L6
			} else if mastering != *d.L6 {
				l6Mixed = true
			}
			l6Count++
		}
		if d.CM29 {
			cm29++
		}
		if d.CM40 {
			cm40++
		}
		if d.SceneRefresh == 1 {
			scenes++
		}
		if d.L5 != nil {
			l5++
		}
		for _, b := range d.Blocks {
			if b.Level == 2 || b.Level == 8 {
				trims++
			}
		}
		if d.L1 == nil {
			bucket.gap = true
			continue
		}
		v := d.L1
		stats.add(*v)
		values := [3]uint16{v.MaxPQ, v.AvgPQ, v.MinPQ}
		if !bucket.present {
			bucket.first = values
			bucket.lo = values
			bucket.hi = values
			bucket.present = true
		}
		bucket.last = values
		for j := range 3 {
			bucket.lo[j] = min(bucket.lo[j], values[j])
			bucket.hi[j] = max(bucket.hi[j], values[j])
		}
	}
	if stats.count == 0 {
		return nil, ErrNoL1
	}
	floor, _ := NitsToPQ(100)
	ceiling := max(floor, float64(max(stats.max, stats.avg))/4095)
	// Unusual L1 ordering remains visible without modifying source values.
	for _, b := range buckets {
		for _, v := range b.hi {
			ceiling = max(ceiling, float64(v)/4095)
		}
	}
	ceiling = min(1, ceiling*1.04)
	canvas := image.NewRGBA(image.Rect(0, 0, 3000, 1200))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	ttf, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(ttf, &opentype.FaceOptions{Size: 25, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	ink := color.RGBA{35, 39, 47, 255}
	grid := color.RGBA{220, 224, 230, 255}
	colors := [3]color.RGBA{{35, 105, 205, 255}, {142, 68, 173, 255}, {45, 50, 60, 255}}
	label := func(x, y int, text string, c color.Color) {
		d := font.Drawer{Dst: canvas, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
		for font.MeasureString(face, text).Ceil() > 2800 && len(text) > 0 {
			_, size := utf8.DecodeLastRuneInString(text)
			text = text[:len(text)-size]
		}
		d.DrawString(text)
	}
	line := func(x0, y0, x1, y1 int, c color.RGBA) {
		dx, dy := x1-x0, y1-y0
		steps := max(abs(dx), abs(dy))
		if steps == 0 {
			canvas.SetRGBA(x0, y0, c)
			return
		}
		for k := 0; k <= steps; k++ {
			x := x0 + dx*k/steps
			y := y0 + dy*k/steps
			canvas.SetRGBA(x, y, c)
			canvas.SetRGBA(x, y+1, c)
		}
	}
	ypos := func(v float64) int { return bottom - int(math.Round(v/ceiling*float64(bottom-top))) }
	lastTickY := bottom + 40
	for _, n := range []float64{0, .01, .1, 1, 10, 50, 100, 200, 400, 600, 1000, 2000, 4000, 10000} {
		p, _ := NitsToPQ(n)
		if p > ceiling {
			continue
		}
		y := ypos(p)
		if lastTickY-y < 30 {
			continue
		}
		lastTickY = y
		line(left, y, right, y, grid)
		label(20, y+8, fmt.Sprintf("%g nits", n), ink)
	}
	ticks := min(uint64(5), end-start)
	for j := uint64(0); j <= ticks; j++ {
		x := left + (right-left)/2
		if ticks > 0 {
			x = left + (right-left)*int(j)/int(ticks)
		}
		line(x, top, x, bottom, grid)
		index := start
		if ticks > 0 {
			index = start + (end-start)*j/ticks
		}
		label(x-25, bottom+42, fmt.Sprint(index), ink)
	}
	for series := range 3 {
		previous := -1
		for x, b := range buckets {
			if x%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if !b.present {
				if b.gap {
					previous = -1
				}
				continue
			}
			px := left + x
			lo, hi := ypos(float64(b.lo[series])/4095), ypos(float64(b.hi[series])/4095)
			line(px, lo, px, hi, colors[series])
			if lo == hi {
				for dy := -3; dy <= 3; dy++ {
					for dx := -3; dx <= 3; dx++ {
						if dx*dx+dy*dy <= 9 {
							canvas.SetRGBA(px+dx, lo+dy, colors[series])
						}
					}
				}
			}
			if previous >= 0 && !b.gap {
				p := buckets[previous]
				line(left+previous, ypos(float64(p.last[series])/4095), px, ypos(float64(b.first[series])/4095), colors[series])
			}
			previous = x
			if b.gap {
				previous = -1
			}
		}
	}
	title := o.Title
	if title == "" {
		title = "Dolby Vision L1 brightness metadata"
	}
	label(left, 55, title, ink)
	profileNames := []string{}
	for p, present := range profiles {
		if present {
			profileNames = append(profileNames, fmt.Sprint(p))
		}
	}
	label(left, 99, fmt.Sprintf("%s  |  L1 samples %d / %d plotted (%d total)  |  coverage %s  |  profiles %s", a.Timeline, stats.count, end-start+1, a.length(), a.coverage(), strings.Join(profileNames, ", ")), ink)
	label(left, 139, fmt.Sprintf("CM2.9 %d · CM4.0 %d · refresh flags %d · L5 samples %d · trim blocks %d", cm29, cm40, scenes, l5, trims), ink)
	for i, name := range []string{"L1 maximum", "L1 average", "L1 minimum"} {
		x := left + i*550
		line(x, 1080, x+60, 1080, colors[i])
		label(x+80, 1088, name, ink)
	}
	s := stats.result()
	scope := "full timeline"
	if start != 0 || end != uint64(a.length()-1) {
		scope = "selected range"
	}
	label(left, 1140, fmt.Sprintf("%s [%d–%d] · peak %.2f nits · PQ-mean peak %.2f nits · metadata, not measured pixels", scope, start, end, s.PeakNits, s.PQMeanPeakNits), ink)
	sourceText := "Source mastering unknown"
	if sourceCount > 0 {
		sourceText = fmt.Sprintf("Source mastering PQ %d–%d (%d samples)", source[0], source[1], sourceCount)
		if sourceMixed {
			sourceText += "; mixed, first shown"
		}
	}
	l6Text := "L6 absent"
	if l6Count > 0 {
		l6Text = fmt.Sprintf("L6 mastering %g–%d nits, CLL/FALL %d/%d (%d samples)", float64(mastering.MinMastering)/10000, mastering.MaxMastering, mastering.MaxCLL, mastering.MaxFALL, l6Count)
		if l6Mixed {
			l6Text += "; mixed, first shown"
		}
	}
	label(left, 1180, sourceText+" | "+l6Text, ink)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return canvas, nil
}
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
