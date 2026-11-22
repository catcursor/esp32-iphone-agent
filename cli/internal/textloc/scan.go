package textloc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"strings"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/portableocr"
)

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type Box struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Item struct {
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence,omitempty"` // engine score in [0,1], when available
	Box        Box      `json:"box"`
	Center     Point    `json:"center"` // pixels of the delivered screenshot
	Tap        Point    `json:"tap"`    // pixels of the original capture, before crop/resize
}

type Result struct {
	Status  string `json:"status"`
	Engine  string `json:"engine,omitempty"`
	Items   []Item `json:"items"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
}

type recognition struct {
	lines   [][]word
	engine  string
	warning string
	err     error
}

// Scan reads the exact PNG or JPEG delivered to the caller. size is its
// encoded size; region is the source rectangle in full-screen tap pixels.
func Scan(ctx context.Context, data []byte, size image.Point, region image.Rectangle) Result {
	return summarize(readImage(ctx, data, "", ""), size, region)
}

func summarize(read recognition, size image.Point, region image.Rectangle) Result {
	out := Result{Status: "empty", Engine: read.engine, Items: []Item{}, Warning: read.warning}
	if read.err != nil {
		out.Status, out.Error = "error", read.err.Error()
		if errors.Is(read.err, portableocr.ErrUnavailable) {
			out.Status = "unavailable"
		}
		return out
	}
	if size.X <= 0 || size.Y <= 0 || region.Empty() {
		out.Status, out.Error = "error", "invalid screenshot dimensions"
		return out
	}
	for _, line := range read.lines {
		for _, w := range line {
			b := w.Box.Intersect(image.Rectangle{Max: size})
			if b.Empty() || strings.TrimSpace(w.Text) == "" {
				continue
			}
			x, y := b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2
			// Map the center of each output pixel into the corresponding source pixel.
			tx := region.Min.X + (2*x+1)*region.Dx()/(2*size.X)
			ty := region.Min.Y + (2*y+1)*region.Dy()/(2*size.Y)
			out.Items = append(out.Items, Item{
				Text: w.Text, Confidence: w.Confidence, Box: Box{b.Min.X, b.Min.Y, b.Dx(), b.Dy()},
				Center: Point{x, y}, Tap: Point{tx, ty},
			})
		}
	}
	if len(out.Items) > 0 {
		out.Status = "ok"
	}
	return out
}

func readImage(ctx context.Context, data []byte, nativePath, label string) recognition {
	rapidCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	raw, rapidErr := portableocr.Recognize(rapidCtx, data)
	cancel()
	if rapidErr == nil {
		var lines [][]word
		lines, rapidErr = parseRapidOCR(raw)
		if rapidErr == nil {
			return recognition{lines: lines, engine: "rapidocr"}
		}
	}
	if ctx.Err() != nil {
		return recognition{engine: "rapidocr", err: ctx.Err()}
	}
	nativeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	lines, nativeErr := nativeImage(nativeCtx, nativePath, data, label)
	out := recognition{engine: nativeEngine, lines: lines, err: nativeErr}
	if !errors.Is(rapidErr, portableocr.ErrUnavailable) {
		out.warning = fmt.Sprintf("RapidOCR failed: %v", rapidErr)
		// An installed engine failed, even if no fallback is installed.
		if nativeErr != nil {
			out.err = fmt.Errorf("RapidOCR: %v; %s: %v", rapidErr, nativeEngine, nativeErr)
			out.warning = ""
		}
	}
	return out
}

func nativeImage(ctx context.Context, path string, data []byte, label string) ([][]word, error) {
	if path == "" {
		pattern := "esp32agent-ocr-*.png"
		if bytes.HasPrefix(data, []byte{0xff, 0xd8}) {
			pattern = "esp32agent-ocr-*.jpg"
		}
		f, err := os.CreateTemp("", pattern)
		if err != nil {
			return nil, err
		}
		path = f.Name()
		defer os.Remove(path)
		_, writeErr := f.Write(data)
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return nil, err
		}
	}
	return recognize(ctx, path, label)
}

func parseRapidOCR(raw []byte) ([][]word, error) {
	var items []struct {
		Text       string      `json:"txt"`
		Box        [][]float64 `json:"box"`
		Confidence *float64    `json:"score"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decoding RapidOCR results: %w", err)
	}
	if items == nil {
		return nil, errors.New("expected an array of RapidOCR results")
	}
	var lines [][]word
	for _, item := range items {
		if len(item.Box) != 4 || strings.TrimSpace(item.Text) == "" ||
			(item.Confidence != nil && (*item.Confidence < 0 || *item.Confidence > 1)) {
			return nil, errors.New("invalid RapidOCR detection")
		}
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, pt := range item.Box {
			if len(pt) != 2 || math.Abs(pt[0]) > math.MaxInt32 || math.Abs(pt[1]) > math.MaxInt32 {
				return nil, errors.New("invalid RapidOCR polygon")
			}
			minX, minY = min(minX, pt[0]), min(minY, pt[1])
			maxX, maxY = max(maxX, pt[0]), max(maxY, pt[1])
		}
		box := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY)))
		if box.Empty() {
			return nil, errors.New("empty RapidOCR bounding box")
		}
		lines = append(lines, []word{{Text: item.Text, Box: box, Confidence: item.Confidence}})
	}
	return lines, nil
}
