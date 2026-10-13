package dovi

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
)

type plotData struct {
	stats                       statisticAccumulator
	maxMin                      uint16
	ceiling                     float64
	missing                     int
	leftCaptions, rightCaptions [3]string
}

func (d plotData) labels() [3]string {
	s := d.stats.result()
	return [3]string{
		fmt.Sprintf("Maximum (MaxCLL: %.2f nits, avg: %.2f nits)", s.PeakNits, s.PQMeanPeakNits),
		fmt.Sprintf("Average (MaxFALL: %.2f nits, avg: %.2f nits)", s.MaximumAverageNits, s.PQMeanAverageNits),
		fmt.Sprintf("Minimum (max: %.06f nits)", codeNits(float64(d.maxMin))),
	}
}

func collectPlot(ctx context.Context, a *Analysis, start, end int, budget int64) (plotData, error) {
	var out plotData
	var profiles [9]bool
	var mel, fel bool
	var cm29, cm40, scenes int
	masters := map[[2]uint16]bool{}
	l6seen := map[L6]bool{}
	var l6order []L6
	var l5min, l5max [4]uint16
	var l5count int
	var l2 [4096]bool
	var l8 [2001]bool
	var l9 [256]int
	for i := start; i <= end; i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return out, err
			}
		}
		r := a.record(i)
		if r == nil {
			out.missing++
			continue
		}
		profiles[r.Profile] = true
		mel = mel || r.EnhancementLayer == "MEL"
		fel = fel || r.EnhancementLayer == "FEL"
		d := r.DM
		if d == nil {
			out.missing++
			continue
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
		if d.Base != nil {
			k := [2]uint16{d.Base.SourceMinPQ, d.Base.SourceMaxPQ}
			if !masters[k] {
				budget -= 256
				if budget < 0 {
					return out, ErrLimit
				}
				masters[k] = true
			}
		}
		if d.L6 != nil && !l6seen[*d.L6] {
			budget -= 512
			if budget < 0 {
				return out, ErrLimit
			}
			l6seen[*d.L6] = true
			l6order = append(l6order, *d.L6)
		}
		if d.L5 != nil {
			v := [4]uint16{d.L5.Top, d.L5.Bottom, d.L5.Left, d.L5.Right}
			for j, n := range v {
				if l5count == 0 || n < l5min[j] {
					l5min[j] = n
				}
				l5max[j] = max(l5max[j], n)
			}
			l5count++
		}
		for _, b := range d.Blocks {
			switch b.Level {
			case 2:
				l2[b.Values[0]] = true
			case 8:
				l8[trimTargetNits(b.Values[0])] = true
			case 9:
				l9[b.Values[0]]++
			}
		}
		if d.L1 == nil {
			out.missing++
			continue
		}
		out.stats.add(*d.L1)
		out.maxMin = max(out.maxMin, d.L1.MinPQ)
	}
	if out.stats.count == 0 {
		return out, ErrNoL1
	}
	out.ceiling = float64(out.stats.max) / 4095
	var names []string
	for i, present := range profiles {
		if present {
			v := fmt.Sprint(i)
			if i == 7 {
				var sub []string
				if fel {
					sub = append(sub, "FEL")
				}
				if mel {
					sub = append(sub, "MEL")
				}
				v += " (" + strings.Join(sub, ", ") + ")"
			}
			names = append(names, v)
		}
	}
	profile := "Profile"
	if len(names) > 1 {
		profile += "s"
	}
	profile += ": " + strings.Join(names, ", ")
	version := "1 (CM v2.9)"
	counts := ""
	if cm40 == cm29 {
		version = "2 (CM v4.0)"
	} else if cm40 > 0 {
		version = "1 + 2 (CM 2.9 and 4.0)"
		counts = fmt.Sprintf(" v2.9 count: %d, v4.0 count: %d", cm29, cm40)
	}
	out.leftCaptions[0] = fmt.Sprintf("Frames: %d. %s. Scenes: %d. DM version: %s.%s", end-start+1, profile, scenes, version, counts)
	if out.missing > 0 {
		out.leftCaptions[0] += fmt.Sprintf(" L1: %d/%d; missing metadata shown as gaps.", out.stats.count, end-start+1)
	}
	keys := make([][2]uint16, 0, len(masters))
	for k := range masters {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]uint16) int {
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		return int(a[1]) - int(b[1])
	})
	names = nil
	for _, k := range keys {
		n := math.Round(codeNits(float64(k[0]))*1e6) / 1e6
		peak := math.Round(codeNits(float64(k[1]))/1000) * 1000
		names = append(names, fmt.Sprintf("%.4f/%g nits", n, peak))
	}
	out.leftCaptions[1] = "RPU mastering display: " + strings.Join(names, ", ")
	if len(keys) == 0 {
		out.leftCaptions[1] += "unknown"
	}
	if cm40 > 0 {
		aliases := [...]string{"DCI-P3 D65", "BT.709", "BT.2020", "SMPTE-C", "BT.601", "DCI-P3", "ACES", "S-Gamut", "S-Gamut-3.Cine"}
		names = nil
		for i, n := range l9 {
			if n > 0 {
				name := aliases[0]
				if i < len(aliases) {
					name = aliases[i]
				}
				if n < cm40 {
					name += fmt.Sprintf(" (%d)", n)
				}
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			out.leftCaptions[1] += " - " + strings.Join(names, ", ")
		}
	}
	names = nil
	for _, v := range l6order[:min(2, len(l6order))] {
		names = append(names, fmt.Sprintf("Mastering display: %.4f/%d nits. MaxCLL: %d nits, MaxFALL: %d nits", float64(v.MinMastering)/10000, v.MaxMastering, v.MaxCLL, v.MaxFALL))
	}
	l6 := "None"
	if len(names) > 0 {
		l6 = strings.Join(names, ". ")
	}
	if len(l6order) > 2 {
		l6 += fmt.Sprintf(". Total different: %d", len(l6order))
	}
	out.leftCaptions[2] = "L6 metadata: " + l6
	names = nil
	for i, name := range []string{"top", "bottom", "left", "right"} {
		value := "N/A"
		if l5count > 0 {
			value = fmt.Sprint(l5min[i])
			if l5min[i] != l5max[i] {
				value += fmt.Sprintf("..%d", l5max[i])
			}
		}
		names = append(names, name+"="+value)
	}
	right := []string{"L5 offsets: " + strings.Join(names, ", ")}
	names = nil
	for pq, present := range l2 {
		if present {
			names = append(names, fmt.Sprintf("%g nits", math.Round(codeNits(float64(pq))/100)*100))
		}
	}
	if len(names) > 0 {
		right = append(right, "L2 trims: "+strings.Join(names, ", "))
	}
	names = nil
	if cm40 > 0 {
		for n, present := range l8 {
			if present {
				names = append(names, fmt.Sprintf("%d nits", n))
			}
		}
	}
	if len(names) > 0 {
		right = append(right, "L8 trims: "+strings.Join(names, ", "))
	}
	copy(out.rightCaptions[3-len(right):], right)
	return out, nil
}

func trimTargetNits(index int64) int {
	switch index {
	case 16, 18, 21:
		return 48
	case 42:
		return 108
	case 24, 25:
		return 300
	case 27, 28:
		return 600
	case 48, 49:
		return 1000
	case 37, 38:
		return 2000
	default:
		return 100
	}
}

// tickInterval follows Plotters' integer key-point selection.
func tickInterval(count, maxPoints int) int {
	scale := 1
	for math.Ceil(float64(count)/float64(scale)) > float64(maxPoints) {
		next := scale * 10
		for _, s := range []int{scale * 2, scale * 5, next} {
			scale = s
			if math.Ceil(float64(count)/float64(s)) < float64(maxPoints) {
				return scale
			}
		}
		scale = next
	}
	return scale
}

// plotTicks follows dovi_tool PlotCoord: fixed low-nit points and a 0.04-PQ
// grid above 100 nits, rounded to whole nits including the observed maximum.
func plotTicks(ceiling float64) []float64 {
	midpoint, _ := NitsToPQ(100)
	var grid, points []float64
	if midpoint+.04 < ceiling {
		for v := midpoint; v < ceiling; v += .04 {
			grid = append(grid, v)
		}
	}
	if len(grid) > 0 {
		step := tickInterval(len(grid)-1, 12)
		for i := 0; i < len(grid); i += step {
			v := grid[i]
			if math.Abs(v-midpoint) >= .04 && math.Abs(ceiling-v) >= .04 {
				points = append(points, v)
			}
		}
	}
	points = append(points, ceiling)
	for i, v := range points {
		n, _ := PQToNits(v)
		points[i], _ = NitsToPQ(math.Round(n))
	}
	for _, n := range []float64{.01, .1, .5, 1, 2.5, 5, 10, 25, 50, 100} {
		v, _ := NitsToPQ(n)
		points = append(points, v)
	}
	slices.Sort(points)
	return slices.Compact(points)
}
