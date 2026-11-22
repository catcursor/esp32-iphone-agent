package daemon

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/pointer"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/proto"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/video"
)

// Render the simulated board's relative pointer on real test frames; the
// production verifier must see it leave and return before a tap can be sent.
func pointerMirror(c *Conn, board *proto.FakeBoard, background image.Image, span image.Point) {
	size := background.Bounds().Size()
	c.mu.Lock()
	c.ffmpeg = "simulated"
	c.mu.Unlock()
	start := time.Now()
	c.onStatus([]byte(`{"ap":"streaming","up":1000,"vage":0}`))
	c.onFrame(video.Frame{Img: background, TS: 1000})
	previous := board.Handle
	var pos image.Point
	board.Handle = func(cmd string) string {
		result := previous(cmd)
		if cmd == "home" {
			pos = image.Point{}
		} else if strings.HasPrefix(cmd, "move ") {
			var dx, dy int
			fmt.Sscanf(cmd, "move %d %d", &dx, &dy)
			pos.X = max(0, min(span.X, pos.X+dx))
			pos.Y = max(0, min(span.Y, pos.Y+dy))
		} else {
			return result
		}
		// A mirrored frame is captured after the board moves. Allow one frame
		// interval here as well, including on hosts with coarse clock ticks.
		time.Sleep(20 * time.Millisecond)
		img := image.NewRGBA(background.Bounds())
		draw.Draw(img, img.Bounds(), background, background.Bounds().Min, draw.Src)
		cx, cy := size.X*pos.X/span.X, size.Y*pos.Y/span.Y
		r := max(8, size.X/32)
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if dx*dx+dy*dy <= r*r {
					img.SetRGBA(cx+dx, cy+dy, color.RGBA{R: 150, G: 15, B: 70, A: 255})
				}
			}
		}
		c.onFrame(video.Frame{Img: img, TS: 1000 + uint32(time.Since(start).Milliseconds())})
		return result
	}
}

func TestLocatedTapSendsOneClickOnlyForResolvedAction(t *testing.T) {
	s, board := directBoard(t)
	c, err := s.conn(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 300, 420))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for k := -4; k <= 4; k++ {
		ink := image.NewUniform(color.Gray{Y: 180})
		if k == 0 {
			ink = image.Black
		}
		x, y := 150+25*k, 210+25*k
		draw.Draw(img, image.Rect(x, 110, x+1, 311), ink, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(50, y, 251, y+1), ink, image.Point{}, draw.Src)
	}
	pointerMirror(c, board, img, image.Pt(300, 420))
	c.pointer(s.Dir).Span = &pointer.Span{W: 300, H: 420}
	req := Request{Cmd: "tap-grid", Step: []float64{5, 5}, Pts: []float64{-10, 5}, Locate: true}
	before := len(board.Commands)
	r := s.Do(t.Context(), req)
	if r.Status != "ok" {
		t.Fatal(r)
	}
	data := r.Data.(map[string]any)
	if data["x"] != 100 || data["y"] != 185 || data["tapped"] != false || len(board.Commands) != before {
		t.Fatal(data, board.Commands)
	}
	req.Locate, req.Pts[0] = false, -99
	if r := s.Do(t.Context(), req); r.Status != "error" || len(board.Commands) != before {
		t.Fatal(r, board.Commands)
	}
	req.Pts[0] = -10
	if r := s.Do(t.Context(), req); r.Status != "ok" || r.Data.(map[string]any)["tapped"] != true {
		t.Fatal(r)
	}
	commands := board.Commands[before:]
	click := slices.Index(commands, "click")
	if click < 0 || slices.Contains(commands[click+1:], "click") || !slices.Contains(commands[:click], "move -12 0") {
		t.Fatal("expected one click", commands)
	}
	before = len(board.Commands)
	if r := s.Do(t.Context(), Request{Cmd: "tap", Pts: []float64{80, 140}}); r.Status != "ok" || slices.Contains(board.Commands[before:], "move -12 0") {
		t.Fatal("same session repeated the pointer check", r, board.Commands[before:])
	}
}

func TestTargetChangedWhenSmallLabelDisappears(t *testing.T) {
	old, now := image.NewRGBA(image.Rect(0, 0, 360, 780)), image.NewRGBA(image.Rect(0, 0, 360, 780))
	draw.Draw(old, image.Rect(103, 205, 119, 212), image.White, image.Point{}, draw.Src)
	if !targetChanged(old, now, image.Pt(110, 208)) {
		t.Fatal("a change at the tap target went unnoticed")
	}
}

func TestExplicitPointerVerificationReusedOnlyOnCurrentSessionAndSize(t *testing.T) {
	s, b := directBoard(t)
	c, err := s.conn(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 300, 480))
	pointerMirror(c, b, img, img.Bounds().Size())
	c.pointer(s.Dir).Span = &pointer.Span{W: 300, H: 480}
	r := s.Do(t.Context(), Request{Cmd: "calibrate", Verify: true})
	report, ok := r.Data.(*pointer.Verification)
	if r.Status != "ok" || !ok || !report.Verified || len(report.Samples) != 2 || !c.pointerChecked() {
		t.Fatal(r, b.Commands)
	}
	if slices.Contains(b.Commands, "click") || slices.Contains(b.Commands, "press") {
		t.Fatal(b.Commands)
	}
	before := len(b.Commands)
	if r = s.Do(t.Context(), Request{Cmd: "tap", Pts: []float64{80, 140}}); r.Status != "ok" || slices.Contains(b.Commands[before:], "move -12 0") {
		t.Fatal(r, b.Commands[before:])
	}
	stale := context.WithValue(t.Context(), inputSessionKey{}, c.Links())
	b.SkipB()
	b.Status(`{}`)
	for end := time.Now().Add(time.Second); c.connected() && time.Now().Before(end); {
		time.Sleep(time.Millisecond)
	}
	if _, err = s.conn(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	c.onFrame(video.Frame{Img: img})
	if c.pointerChecked() {
		t.Fatal("reused a verification from a previous connection")
	}
	if _, err = c.Send(stale, "click"); err == nil {
		t.Fatal("sent a pending click on a different link")
	}
	c.mu.Lock()
	c.checkedSize, c.checkedLink = img.Bounds().Size(), c.links
	c.mu.Unlock()
	c.onFrame(video.Frame{Img: image.NewRGBA(image.Rect(0, 0, 480, 300))})
	c.onFrame(video.Frame{Img: img})
	if c.pointerChecked() {
		t.Fatal("reused a verification after the display rotated")
	}
}
