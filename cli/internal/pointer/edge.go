package pointer

import "math"

// edgeCenter compares frames from opposite straight edges. Normal coordinates
// run inward from the edge, tangent coordinates run along it. Test the visible
// arc only: the OS can clip half of the cursor outside the mirrored frame.
func edgeCenter(on, off Gray, top bool) (Point, bool) {
	normal, tangent := on.W, on.H
	if top {
		normal, tangent = on.H, on.W
	}
	index := func(n, t int) int {
		if top {
			return n*on.W + t
		}
		return t*on.W + n
	}
	minSide := min(on.W, on.H)
	maxR := max(8, minSide/20)
	depth := min(normal, 3*maxR)
	changed := make([]bool, depth*tangent)
	for t := maxR; t < tangent-maxR; t++ {
		for n := 0; n < depth; n++ {
			i := index(n, t)
			d := int(on.Px[i]) - int(off.Px[i])
			changed[t*depth+n] = d >= 10 || d <= -10
		}
	}
	best := 0.0
	var bestN, bestT float64
	for seed, active := range changed {
		if !active {
			continue
		}
		queue := []int{seed}
		changed[seed] = false
		minN, maxN, minT, maxT := depth, 0, tangent, 0
		for k := 0; k < len(queue); k++ {
			j := queue[k]
			n, t := j%depth, j/depth
			minN, maxN, minT, maxT = min(minN, n), max(maxN, n), min(minT, t), max(maxT, t)
			for _, off := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				u, v := n+off[0], t+off[1]
				if u < 0 || u >= depth || v < 0 || v >= tangent {
					continue
				}
				i := v*depth + u
				if changed[i] {
					changed[i] = false
					queue = append(queue, i)
				}
			}
		}
		r := float64(maxT-minT+1) / 2
		if r < 4 || r > float64(maxR) || len(queue) < 25 || minN > maxR/2 {
			continue
		}
		ct := float64(minT+maxT) / 2
		cn := float64(minN+maxN) / 2
		if minN <= 1 {
			cn = float64(maxN) + 0.5 - r
		}
		if cn < -2 || cn > float64(maxR) {
			continue
		}
		// Compare the component with a disc truncated by the image boundary.
		inside, area := 0, 0
		for _, j := range queue {
			dn, dt := float64(j%depth)-cn, float64(j/depth)-ct
			if dn*dn+dt*dt <= r*r {
				inside++
			}
		}
		for t := minT; t <= maxT; t++ {
			reach := math.Sqrt(max(0, r*r-(float64(t)-ct)*(float64(t)-ct)))
			area += max(0, int(math.Floor(cn+reach))-max(0, int(math.Ceil(cn-reach)))+1)
		}
		score := float64(inside) / float64(max(1, area+len(queue)-inside))
		if score > best {
			best, bestN, bestT = score, max(0, cn), ct
		}
	}
	x, y := bestN, bestT
	if top {
		x, y = y, x
	}
	return Point{x / float64(on.W), y / float64(on.H)}, best >= 0.75
}
