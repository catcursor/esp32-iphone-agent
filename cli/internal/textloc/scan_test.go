package textloc

import (
	"encoding/json"
	"errors"
	"image"
	"testing"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/portableocr"
)

func TestScanMapsTextToSource(t *testing.T) {
	raw := []byte(`[{"txt":"保存图片","box":[[8.2,22.1],[40,20.4],[42.5,36.8],[9,38.8]],"score":0.98}]`)
	lines, err := parseRapidOCR(raw)
	if err != nil {
		t.Fatal(err)
	}
	out := summarize(recognition{engine: "rapidocr", lines: lines}, image.Pt(50, 40), image.Rect(7, 21, 107, 101))
	want := Item{Text: "保存图片", Box: Box{8, 20, 35, 19}, Center: Point{25, 29}, Tap: Point{58, 80}}
	if len(out.Items) != 1 || out.Items[0].Confidence == nil || *out.Items[0].Confidence != .98 {
		t.Fatalf("missing confidence: %+v", out.Items)
	}
	want.Confidence = out.Items[0].Confidence
	if out.Status != "ok" || len(out.Items) != 1 || out.Items[0] != want {
		t.Fatalf("wrong screenshot/source geometry: %+v", out)
	}
	unchanged := summarize(recognition{lines: lines}, image.Pt(50, 40), image.Rect(0, 0, 50, 40))
	if unchanged.Items[0].Center != unchanged.Items[0].Tap {
		t.Fatalf("full-size tap should use screenshot coordinates: %+v", unchanged.Items[0])
	}
}

func TestScanEmptyAndFailed(t *testing.T) {
	for _, tc := range []struct {
		read   recognition
		status string
	}{{recognition{}, "empty"}, {recognition{err: portableocr.ErrUnavailable}, "unavailable"}, {recognition{err: errors.New("engine crashed")}, "error"}} {
		out := summarize(tc.read, image.Pt(17, 26), image.Rect(0, 0, 17, 26))
		raw, _ := json.Marshal(out.Items)
		if out.Status != tc.status || string(raw) != "[]" || (out.Error != "") != (tc.read.err != nil) {
			t.Fatalf("wrong status for %+v: %+v", tc, out)
		}
	}
	for _, invalid := range []string{`null`, `not JSON`, `[{"txt":"x","box":[[2,2]]}]`, `[{"txt":"x","box":[[1,1],[1,1],[1,1],[1,1]]}]`} {
		if _, err := parseRapidOCR([]byte(invalid)); err == nil {
			t.Fatalf("accepted malformed OCR output %s", invalid)
		}
	}
	if lines, err := parseRapidOCR([]byte("[]")); err != nil || len(lines) != 0 {
		t.Fatalf("empty recognition: %v, %v", lines, err)
	}
}
