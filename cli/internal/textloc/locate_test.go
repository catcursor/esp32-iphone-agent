package textloc

import (
	"image"
	"testing"
)

func TestLocateWords(t *testing.T) {
	first := word{Text: "保", Box: image.Rect(10, 20, 34, 50)}
	second := word{Text: "存", Box: image.Rect(36, 20, 60, 50)}
	label := []word{first, second}
	for _, tc := range []struct {
		name  string
		query string
		lines [][]word
		point image.Point
		ok    bool
	}{
		{"joined characters", "保存", [][]word{label}, image.Pt(35, 35), true},
		{"case and fullwidth", "Save As", [][]word{{{Text: "ＳＡＶＥ", Box: first.Box}, {Text: "as", Box: second.Box}}}, image.Pt(35, 35), true},
		{"ambiguous", "保存", [][]word{label, label}, image.Point{}, false},
		{"cross line", "保存", [][]word{{first}, {second}}, image.Point{}, false},
		{"separate controls", "保存", [][]word{{first, {Text: "存", Box: second.Box.Add(image.Pt(100, 0))}}}, image.Point{}, false},
		{"partial word", "set", [][]word{{{Text: "settings", Box: first.Box}}}, image.Point{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := locateWords(tc.lines, normalize(tc.query))
			if (err == nil) != tc.ok || got != tc.point {
				t.Fatalf("got %v (%v), want %v (ok=%v)", got, err, tc.point, tc.ok)
			}
		})
	}
}
