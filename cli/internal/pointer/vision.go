package pointer

import (
	"image"
	"math"
	"slices"
)

// Gray is a grayscale bitmap (PIL "L" weights), row-major.
type Gray struct {
	W, H int
	Px   []uint8
}

// ToGray converts a frame like PIL's convert("L").
func ToGray(img image.Image) Gray {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	g := Gray{W: w, H: h, Px: make([]uint8, w*h)}
	lum := func(r, gg, bb int) uint8 { return uint8((r*299 + gg*587 + bb*114 + 500) / 1000) }
	if rgba, ok := img.(*image.RGBA); ok {
		for y := 0; y < h; y++ {
			row := rgba.Pix[y*rgba.Stride:]
			for x := 0; x < w; x++ {
				g.Px[y*w+x] = lum(int(row[4*x]), int(row[4*x+1]), int(row[4*x+2]))
			}
		}
		return g
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			g.Px[y*w+x] = lum(int(r>>8), int(gg>>8), int(bb>>8))
		}
	}
	return g
}

// Point is a screen position as fractions (0..1, origin top-left).
type Point struct{ X, Y float64 }

// Locate finds the pointer in `before`, given it moved right by `shift` (fraction of width) in `after`.
//
// Only the pointer moved between the two frames, so the changed pixels within `reach` of `near` are the
// two exposed sides of the moving disc. Fit their circular contours to locate its original centre.
func Locate(before, after image.Image, near Point, shift float64, reach Point) (Point, bool) {
	return LocateBack(before, after, nil, near, shift, reach)
}

// LocateBack is Locate with back, a frame after the pointer moved back where it was in before: only pixels that
// changed there and back count, so a clock's second hand or an animation elsewhere is not taken for the pointer
// (2026-11-06: the Clock icon next to the calibration stop). back nil: as Locate.
func LocateBack(before, after, back image.Image, near Point, shift float64, reach Point) (Point, bool) {
	if before == nil || after == nil || before.Bounds().Size() != after.Bounds().Size() {
		return Point{}, false
	}
	if back != nil && back.Bounds().Size() != before.Bounds().Size() {
		back = nil
	}
	a, b := ToGray(before), ToGray(after)
	var c *Gray
	if back != nil {
		g := ToGray(back)
		c = &g
	}
	w, h := a.W, a.H
	fw, fh := float64(w), float64(h)
	left := max(0, int((near.X-reach.X)*fw))
	top := max(int(0.05*fh), int((near.Y-reach.Y)*fh)) // skip the status bar
	right, bottom := min(w, int((near.X+reach.X)*fw)), min(h, int((near.Y+reach.Y)*fh))
	if right <= left || bottom <= top {
		return Point{}, false
	}
	var pts [][2]int
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			i := y*w + x
			out := int(a.Px[i]) - int(b.Px[i])
			if out > -7 && out < 7 {
				continue
			}
			if c != nil {
				back := int(c.Px[i]) - int(b.Px[i])
				if out*back <= 0 || (back > -7 && back < 7) {
					continue
				}
			}
			pts = append(pts, [2]int{x, y})
		}
	}
	pts = biggestBlob(pts, max(4, w/80))
	return fitNudgedCircle(a, b, pts, shift)
}

// fitNudgedCircle fits the two exposed crescents of a horizontally moving disc.
// Row prefix sums make it cheap to try different radii and actual distances.
// The sign of the command identifies which fitted circle is the starting one.
func fitNudgedCircle(a, b Gray, points [][2]int, shift float64) (Point, bool) {
	if len(points) < 16 || shift == 0 {
		return Point{}, false
	}
	xs, ys := make([]int, len(points)), make([]int, len(points))
	brightened := 0
	for i, p := range points {
		xs[i], ys[i] = p[0], p[1]
		pos := p[1]*a.W + p[0]
		if b.Px[pos] > a.Px[pos] {
			brightened++
		}
	}
	// Exposing and covering the background produces opposite changes. A
	// stationary disc that merely blinks can otherwise fit a broad pair of arcs.
	if min(brightened, len(points)-brightened) < max(4, len(points)/40) {
		return Point{}, false
	}
	slices.Sort(xs)
	slices.Sort(ys)
	trim := len(points) / 50
	left, right := xs[trim], xs[len(xs)-1-trim]
	top, bottom := ys[trim], ys[len(ys)-1-trim]
	if right-left > a.W*15/100 || bottom-top > a.H*7/100 {
		return Point{}, false
	}
	radius := max(3, (bottom-top+2)/2)
	padding := radius
	box := image.Rect(left-padding, top-padding, right+padding+1, bottom+padding+1).Intersect(image.Rect(0, 0, a.W, a.H))
	stride := box.Dx() + 1
	counts := make([]int, stride*box.Dy())
	total := 0
	for y := box.Min.Y; y < box.Max.Y; y++ {
		row := counts[(y-box.Min.Y)*stride:]
		for x := box.Min.X; x < box.Max.X; x++ {
			i := y*a.W + x
			row[x-box.Min.X+1] = row[x-box.Min.X]
			if d := int(a.Px[i]) - int(b.Px[i]); d >= 7 || d <= -7 {
				row[x-box.Min.X+1]++
			}
		}
		total += row[stride-1]
	}
	// A horizontal slice of the symmetric difference is two disjoint intervals.
	score := func(x, y, r, dx int) float64 {
		if x-r < box.Min.X || x+dx+r >= box.Max.X || y-r < box.Min.Y || y+r >= box.Max.Y {
			return 0
		}
		hitLeft, hitRight, area := 0, 0, 0
		for dy := -r; dy <= r; dy++ {
			rx := int(math.Sqrt(float64(r*r - dy*dy)))
			l, h := x-rx-box.Min.X, x+rx+1-box.Min.X
			n := min(dx, h-l)
			row := counts[(y+dy-box.Min.Y)*stride:]
			hitLeft += row[l+n] - row[l]
			hitRight += row[h+dx] - row[h+dx-n]
			area += n
		}
		if min(hitLeft, hitRight)*3 < area {
			return 0
		}
		return 2 * float64(hitLeft+hitRight) / float64(total+2*area)
	}
	mx, my := (left+right)/2, (top+bottom)/2
	commanded := max(1, int(math.Round(math.Abs(shift)*float64(a.W))))
	// A rough box only seeds the fit. A bright line crossing the disc can
	// pull both its observed bounds and its centroid away from the true centre.
	best := 0.0
	var bestX, bestY, bestR, bestD int
	search := func(x0, x1, y0, y1, r0, r1, d0, d1, step int) {
		for r := max(3, r0); r <= r1; r += step {
			for dx := max(1, d0); dx <= d1; dx += step {
				for y := y0; y <= y1; y += step {
					for mid := x0; mid <= x1; mid += step {
						x := mid - dx/2
						if s := score(x, y, r, dx); s > best {
							best, bestX, bestY, bestR, bestD = s, x, y, r, dx
						}
					}
				}
			}
		}
	}
	step := max(2, radius/5)
	margin := max(3, radius/2)
	search(mx-margin, mx+margin, my-margin, my+margin, radius*2/3, radius*3/2, 1, min(commanded*2+3, 3*radius), step)
	if best == 0 {
		return Point{}, false
	}
	for step > 1 {
		mid := bestX + bestD/2
		next := max(1, step/2)
		search(mid-step, mid+step, bestY-step, bestY+step, bestR-step, bestR+step, bestD-step, bestD+step, next)
		step = next
	}
	if best < 0.48 {
		return Point{}, false
	}
	if shift < 0 {
		bestX += bestD
	}
	return Point{float64(bestX) / float64(a.W), float64(bestY) / float64(a.H)}, true
}

// biggestBlob keeps the changed pixels of the largest spot: pixels in cells of size cell, cells touching (8-way)
// form one spot. The pointer is a filled disc; a clock's second hand or a badge elsewhere is a separate, thinner one.
func biggestBlob(pts [][2]int, cell int) [][2]int {
	cells := map[[2]int][]int{} // cell → indexes into pts
	for i, p := range pts {
		k := [2]int{p[0] / cell, p[1] / cell}
		cells[k] = append(cells[k], i)
	}
	seen := map[[2]int]bool{}
	var best []int
	for k := range cells {
		if seen[k] {
			continue
		}
		var blob []int
		stack := [][2]int{k}
		seen[k] = true
		for len(stack) > 0 {
			q := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			blob = append(blob, cells[q]...)
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nb := [2]int{q[0] + dx, q[1] + dy}
					if _, ok := cells[nb]; ok && !seen[nb] {
						seen[nb] = true
						stack = append(stack, nb)
					}
				}
			}
		}
		if len(blob) > len(best) {
			best = blob
		}
	}
	out := make([][2]int, len(best))
	for i, j := range best {
		out[i] = pts[j]
	}
	return out
}

// Changed reports whether more than `frac` of a coarse grid of pixels differ clearly between two frames, so a
// moved pointer or a ticking clock doesn't count but a new screen does.
func Changed(a, b image.Image, frac float64) bool {
	if a == nil || b == nil || a.Bounds() != b.Bounds() {
		return a != b
	}
	ga, gb := ToGray(a), ToGray(b)
	const grid = 64
	n, diff := 0, 0
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			i := (gy*ga.H/grid)*ga.W + gx*ga.W/grid
			d := int(ga.Px[i]) - int(gb.Px[i])
			if d > 24 || d < -24 {
				diff++
			}
			n++
		}
	}
	return float64(diff) > frac*float64(n)
}
