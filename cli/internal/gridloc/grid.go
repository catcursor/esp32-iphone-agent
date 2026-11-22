// Package gridloc maps coordinates on a light, axis-aligned Cartesian grid to
// screen pixels. Both zero axes must be visible and darker than the grid lines.
package gridloc

import (
	"errors"
	"image"
	"image/draw"
	"math"
)

// Grid describes an evenly spaced grid in pixels relative to the image's top
// left corner. Min and Max are the centers of the outermost measured lines.
type Grid struct {
	OriginX, OriginY float64
	StepX, StepY     float64
	MinX, MaxX       float64
	MinY, MaxY       float64
}

// Point converts x (increasing right) and y (increasing up) to screen pixels.
// Units specify the value difference between neighboring visible grid lines.
func (g Grid) Point(x, y, unitsPerGridX, unitsPerGridY float64) (image.Point, error) {
	if !(unitsPerGridX > 0 && unitsPerGridY > 0) || math.IsInf(unitsPerGridX, 0) || math.IsInf(unitsPerGridY, 0) {
		return image.Point{}, errors.New("units per grid line must be finite and positive")
	}
	if !(g.StepX > 0 && g.StepY > 0 && g.MinX < g.OriginX && g.OriginX < g.MaxX && g.MinY < g.OriginY && g.OriginY < g.MaxY) {
		return image.Point{}, errors.New("invalid grid geometry")
	}
	px := g.OriginX + (x/unitsPerGridX)*g.StepX
	py := g.OriginY - (y/unitsPerGridY)*g.StepY
	const eps = 1e-8 // tolerate floating-point round-off at an outer grid line
	if !(px >= g.MinX-eps && px <= g.MaxX+eps && py >= g.MinY-eps && py <= g.MaxY+eps) {
		return image.Point{}, errors.New("target is outside the measured grid or is not finite")
	}
	return image.Pt(int(math.Round(px)), int(math.Round(py))), nil
}

// scan describes parallel lines in the grayscale buffer. A point at line i,
// distance j has address i*nextLine + j*along.
type scan struct {
	pix                     []byte
	lines, length           int
	nextLine, along, offset int
}

func (s scan) contrast(line, j int) int {
	idx := line*s.nextLine + j*s.along
	d := s.offset * s.nextLine
	return int(min(s.pix[idx-d], s.pix[idx+d])) - int(s.pix[idx])
}

type axis struct{ pos, start, end int }

// Find long dark strokes with bright pixels on both sides. A small break is
// allowed where a plotted point, cursor or axis annotation covers the stroke.
func (s scan) axes(short int) ([]axis, bool) {
	var found []axis
	maxGap := max(3, short/20)
	minLength := max(32, short/7)
	for i := s.offset; i < s.lines-s.offset; i++ {
		start, last, count := -1, -1, 0
		for j := 0; j <= s.length; j++ {
			if j < s.length && s.pix[i*s.nextLine+j*s.along] < 128 && s.contrast(i, j) > 60 {
				if start < 0 {
					start = j
				}
				last = j
				count++
				continue
			}
			if start < 0 || (j < s.length && j-last <= maxGap) {
				continue
			}
			length := last - start + 1
			if length >= minLength && 3*count >= 2*length {
				// Adjacent pixels can belong to the same antialiased axis.
				duplicate := false
				for k := len(found) - 1; k >= 0 && i-found[k].pos <= 2*s.offset; k-- {
					a := found[k]
					if min(a.end, last)-max(a.start, start)+1 >= 3*min(length, a.end-a.start+1)/4 {
						duplicate = true
						break
					}
				}
				if !duplicate {
					found = append(found, axis{i, start, last})
				}
				if len(found) > 64 {
					return nil, false
				}
			}
			start, last, count = -1, -1, 0
		}
	}
	return found, true
}

type peak struct{ center, strength float64 }
type fit struct{ origin, step, low, high float64 }

// Use the median cross-section contrast over the perpendicular axis span:
// labels, colored points and browser controls only cover a minority of a line.
func (s scan) peaks(first, last, from, to int) []peak {
	var peaks []peak
	total, moment := 0., 0.
	first = max(first, s.offset)
	last = min(last, s.lines-s.offset-1)
	for i := first; i <= last+1; i++ {
		contrast := 0
		if i <= last {
			var hist [256]int
			// Evenly spread samples include both endpoints.
			samples := min(201, to-from+1)
			for k := 0; k < samples; k++ {
				j := from + k*(to-from)/(samples-1)
				hist[max(0, s.contrast(i, j))]++
			}
			count := 0
			for c, n := range hist {
				count += n
				if count > samples/2 {
					contrast = c
					break
				}
			}
		}
		if contrast >= 12 {
			total += float64(contrast)
			moment += float64(i * contrast)
		} else if total > 0 {
			peaks = append(peaks, peak{moment / total, total})
			total, moment = 0, 0
		}
	}
	return peaks
}

func fitLines(peaks []peak, candidate, offset int) (fit, bool) {
	n := len(peaks)
	if n < 5 {
		return fit{}, false
	}
	strong, runnerUp, zero := 0., 0., 0
	for i, p := range peaks {
		if p.strength > strong {
			runnerUp, strong, zero = strong, p.strength, i
		} else {
			runnerUp = max(runnerUp, p.strength)
		}
	}
	if zero == 0 || zero == n-1 || strong <= 1.45*runnerUp || math.Abs(peaks[zero].center-float64(candidate)) > float64(offset) {
		return fit{}, false
	}
	// Fit all line centers, including the zero axis, in one least-squares fit.
	mid := float64(n-1) / 2
	var mean, num, den float64
	for i, p := range peaks {
		d := float64(i) - mid
		num += d * p.center
		den += d * d
		mean += p.center / float64(n)
	}
	step := num / den
	if step < max(5., float64(2*offset+1)) {
		return fit{}, false
	}
	low := mean - mid*step
	var squared float64
	for i, p := range peaks {
		residual := p.center - (low + float64(i)*step)
		if math.Abs(residual) > max(1.5, .03*step) {
			return fit{}, false
		}
		squared += residual * residual
	}
	if math.Sqrt(squared/float64(n)) > max(.8, .015*step) {
		return fit{}, false
	}
	return fit{low + float64(zero)*step, step, low, low + float64(n-1)*step}, true
}

// Detect measures both axes and the grid spacing. It returns an error for
// multiple qualifying charts, absent or ambiguous axes, and nonuniform lines.
func Detect(img image.Image) (Grid, error) {
	if img == nil {
		return Grid{}, errors.New("empty screenshot")
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	short := min(w, h)
	if short < 48 || w > 12000 || h > 12000 || int64(w)*int64(h) > 32000000 {
		return Grid{}, errors.New("unsupported screenshot dimensions")
	}
	gray := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(gray, gray.Bounds(), img, b.Min, draw.Src)
	offset := max(2, int(math.Round(float64(short)*.006)))
	vertical := scan{gray.Pix, w, h, 1, gray.Stride, offset}
	horizontal := scan{gray.Pix, h, w, gray.Stride, 1, offset}
	vAxes, vOK := vertical.axes(short)
	hAxes, hOK := horizontal.axes(short)
	if !vOK || !hOK {
		return Grid{}, errors.New("too many potential axes")
	}
	var result Grid
	found, candidates := false, 0
	for _, v := range vAxes {
		for _, h := range hAxes {
			if v.pos <= h.start+offset || v.pos >= h.end-offset || h.pos <= v.start+offset || h.pos >= v.end-offset {
				continue
			}
			candidates++
			if candidates > 100 {
				return Grid{}, errors.New("too many intersecting axes")
			}
			x, ok := fitLines(vertical.peaks(h.start-offset, h.end+offset, v.start, v.end), v.pos, offset)
			if !ok {
				continue
			}
			y, ok := fitLines(horizontal.peaks(v.start-offset, v.end+offset, h.start, h.end), h.pos, offset)
			if !ok {
				continue
			}
			if found {
				return Grid{}, errors.New("multiple Cartesian grids detected")
			}
			result = Grid{x.origin, y.origin, x.step, y.step, x.low, x.high, y.low, y.high}
			found = true
		}
	}
	if !found {
		return Grid{}, errors.New("no regular grid with two distinct darker interior axes")
	}
	return result, nil
}
