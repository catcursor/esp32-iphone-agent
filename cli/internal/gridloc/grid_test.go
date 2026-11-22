package gridloc

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

func canvas(w, h int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	return img
}

func drawChart(img *image.Gray, x0, y0, sx, sy int, light, dark uint8) {
	for i := -4; i <= 4; i++ {
		ink := light
		if i == 0 {
			ink = dark
		}
		x := x0 + i*sx
		for y := y0 - 4*sy; y <= y0+4*sy; y++ {
			img.SetGray(x, y, color.Gray{ink})
		}
	}
	for j := -4; j <= 4; j++ {
		ink := light
		if j == 0 {
			ink = dark
		}
		y := y0 + j*sy
		for x := x0 - 4*sx; x <= x0+4*sx; x++ {
			img.SetGray(x, y, color.Gray{ink})
		}
	}
}

func TestGeometry(t *testing.T) {
	for _, tc := range []struct{ x, y, sx, sy int }{{158, 183, 27, 18}, {249, 299, 18, 25}, {216, 310, 34, 35}} {
		img := canvas(460, 650)
		drawChart(img, tc.x, tc.y, tc.sx, tc.sy, 193, 22)
		draw.Draw(img, image.Rect(0, 14, 460, 36), image.Black, image.Point{}, draw.Src)
		// One occluding mark in the plot, away from the origin.
		draw.Draw(img, image.Rect(tc.x+tc.sx-3, tc.y-tc.sy-3, tc.x+tc.sx+4, tc.y-tc.sy+4), image.Black, image.Point{}, draw.Src)
		// A pointer straddling the origin can interrupt both dark strokes.
		draw.Draw(img, image.Rect(tc.x-8, tc.y-8, tc.x+9, tc.y+9), image.White, image.Point{}, draw.Src)
		grid, err := Detect(img)
		if err != nil {
			t.Fatal(tc, err)
		}
		target, err := grid.Point(12.5, 5, 5, 2.5)
		want := image.Pt(int(math.Round(float64(tc.x)+2.5*float64(tc.sx))), tc.y-2*tc.sy)
		if err != nil || target != want {
			t.Fatalf("%v: got %v %v, want %v", tc, target, err, want)
		}
		for _, coord := range [][2]float64{{20, 10}, {-20, -10}} {
			if _, err := grid.Point(coord[0], coord[1], 5, 2.5); err != nil {
				t.Fatal("outer grid line rejected", err)
			}
		}
		for _, args := range [][4]float64{{20.01, 0, 5, 2.5}, {0, -10.01, 5, 2.5}, {math.NaN(), 0, 1, 1}, {0, 0, math.Inf(1), 1}, {0, 0, 1, -1}} {
			if _, err := grid.Point(args[0], args[1], args[2], args[3]); err == nil {
				t.Fatal("accepted", args)
			}
		}
	}
}

func TestRejectUncertainGeometry(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, err := Detect(canvas(300, 400)); err == nil {
			t.Fatal("accepted a blank frame")
		}
	})
	t.Run("table", func(t *testing.T) {
		img := canvas(300, 400)
		drawChart(img, 160, 180, 25, 25, 43, 43)
		if _, err := Detect(img); err == nil {
			t.Fatal("accepted a table with equal-weight lines")
		}
	})
	t.Run("multiple charts", func(t *testing.T) {
		img := canvas(420, 630)
		drawChart(img, 190, 150, 23, 20, 193, 22)
		drawChart(img, 190, 412, 23, 20, 193, 22)
		if _, err := Detect(img); err == nil {
			t.Fatal("accepted multiple complete charts")
		}
	})
	t.Run("irregular", func(t *testing.T) {
		img := canvas(300, 400)
		drawChart(img, 160, 180, 25, 25, 193, 22)
		for y := 80; y <= 280; y++ {
			img.SetGray(135, y, color.Gray{255})
			img.SetGray(140, y, color.Gray{193})
		}
		if _, err := Detect(img); err == nil {
			t.Fatal("accepted a displaced grid line")
		}
	})
	t.Run("ambiguous origin", func(t *testing.T) {
		img := canvas(300, 400)
		drawChart(img, 160, 180, 25, 25, 193, 22)
		for y := 80; y <= 280; y++ {
			img.SetGray(135, y, color.Gray{22})
		}
		if _, err := Detect(img); err == nil {
			t.Fatal("accepted a second dark vertical axis")
		}
	})
}
