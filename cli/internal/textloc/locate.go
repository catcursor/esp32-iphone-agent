// Package textloc locates a visible label using OCR performed on this computer.
package textloc

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type word struct {
	Text       string
	Box        image.Rectangle
	Confidence *float64
}

// Locate returns the center in original screenshot pixels. Duplicate and missing
// matches are errors so callers can decline the click.
func Locate(ctx context.Context, screenshotPath, label string) (image.Point, error) {
	needle := normalize(label)
	if needle == "" {
		return image.Point{}, errors.New("provide a non-empty text label")
	}
	path, err := filepath.Abs(screenshotPath)
	if err != nil {
		return image.Point{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return image.Point{}, err
	}
	read := readImage(ctx, data, path, label)
	if read.err != nil {
		return image.Point{}, read.err
	}
	return locateWords(read.lines, needle)
}

func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(s)), ""))
}

// A Chinese OCR engine may return each character of a single label as a word.
// Keep matches within one line and never join distant screen controls.
func locateWords(lines [][]word, needle string) (image.Point, error) {
	var match image.Rectangle
	for _, line := range lines {
		for start := range line {
			var box image.Rectangle
			remaining := needle
			for i := start; i < len(line); i++ {
				w := line[i]
				part := normalize(w.Text)
				if part == "" || w.Box.Empty() || !strings.HasPrefix(remaining, part) {
					break
				}
				if i > start {
					prev := line[i-1].Box
					gap := max(w.Box.Min.X-prev.Max.X, prev.Min.X-w.Box.Max.X)
					if gap > max(prev.Dy(), w.Box.Dy()) {
						break
					}
				}
				box = box.Union(w.Box)
				remaining = strings.TrimPrefix(remaining, part)
				if remaining == "" {
					if !match.Empty() {
						return image.Point{}, fmt.Errorf("multiple OCR matches for %q; provide a unique visible label", needle)
					}
					match = box
					break
				}
			}
		}
	}
	if match.Empty() {
		return image.Point{}, fmt.Errorf("no OCR match for %q", needle)
	}
	return image.Pt(match.Min.X+match.Dx()/2, match.Min.Y+match.Dy()/2), nil
}

func scriptOf(label string) string {
	switch {
	case strings.ContainsFunc(label, func(r rune) bool { return unicode.In(r, unicode.Hiragana, unicode.Katakana) }):
		return "ja"
	case strings.ContainsFunc(label, func(r rune) bool { return unicode.Is(unicode.Hangul, r) }):
		return "ko"
	case strings.ContainsFunc(label, func(r rune) bool { return unicode.Is(unicode.Han, r) }):
		return "zh"
	default:
		return ""
	}
}
