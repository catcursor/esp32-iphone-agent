package daemon

import (
	"context"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"strings"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/gridloc"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/pointer"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/textloc"
)

// CheckGrid validates the graph coordinates on both sides of the daemon socket.
func (r Request) CheckGrid() error {
	if len(r.Pts) != 2 || len(r.Step) != 2 {
		return errors.New("tap-grid requires X,Y and positive graph units per grid cell (--step)")
	}
	for i, v := range r.Pts {
		if math.IsNaN(v) || math.IsInf(v, 0) || !(r.Step[i] > 0) || math.IsInf(r.Step[i], 0) {
			return errors.New("graph coordinates must be finite and both grid steps must be positive and finite")
		}
	}
	return nil
}

func locateOnFrame(ctx context.Context, frame image.Image, req Request) (image.Point, error) {
	if req.Cmd == "tap-grid" {
		if err := req.CheckGrid(); err != nil {
			return image.Point{}, err
		}
		grid, err := gridloc.Detect(frame)
		if err != nil {
			return image.Point{}, err
		}
		return grid.Point(req.Pts[0], req.Pts[1], req.Step[0], req.Step[1])
	}
	if strings.TrimSpace(req.Text) == "" {
		return image.Point{}, errors.New("tap-text requires a visible label")
	}
	// The local OCR backends accept an image file. Close it before another process opens it on Windows.
	f, err := os.CreateTemp("", "esp32agent-ocr-*.png")
	if err != nil {
		return image.Point{}, err
	}
	defer os.Remove(f.Name())
	encodeErr := png.Encode(f, frame)
	closeErr := f.Close()
	if err := errors.Join(encodeErr, closeErr); err != nil {
		return image.Point{}, err
	}
	ocrCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return textloc.Locate(ocrCtx, f.Name(), req.Text)
}

type mirroredFrame struct {
	image.Image
	boardTS      uint32
	session      int
	decodeBehind bool
}

// Recheck the selected frame after OCR too: another caller can issue an input
// while OCR is reading the encoded image. Never attribute a newer frame to it.
func (f mirroredFrame) freshness(c *Conn) FrameFreshness {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, hasBoardClock := c.status["up"].(float64)
	var captured time.Time
	if hasBoardClock && c.offset != nil && f.boardTS != 0 {
		captured = time.UnixMilli(int64(f.boardTS) + *c.offset)
	}
	switch {
	case f.session != c.links || (f.session != 0 && c.sess == nil) || c.frame == nil ||
		f.Bounds().Size() != c.frame.Bounds().Size() || c.status["ap"] != "streaming" || num(c.status["fz"]) == 1:
		return FrameFreshness{"unconfirmed", "the mirror session or screen changed during capture; take a new screenshot"}
	case !c.lastInput.IsZero() && !captured.After(c.lastInput):
		return FrameFreshness{"unconfirmed", "no timestamped frame captured after the last input yet; wait for the mirror and take another screenshot"}
	case f.decodeBehind:
		return FrameFreshness{"unconfirmed", "the decoder has not caught up to video received from the phone; take another screenshot"}
	default:
		return FrameFreshness{Status: "current"}
	}
}

func (s *Server) currentFrame(ctx context.Context, c *Conn) (mirroredFrame, error) {
	if _, err := s.frame(ctx, c); err != nil {
		return mirroredFrame{}, err
	}
	// A new connection replays buffered video starting at an older keyframe.
	// Let the decoder settle, then catch up to video already received from the board.
	c.Settled(ctx, 250*time.Millisecond, 250*time.Millisecond, 2*time.Second)
	c.mu.Lock()
	var decodedThrough time.Time
	if c.latestVideoTS != 0 && c.offset != nil {
		decodedThrough = time.UnixMilli(int64(c.latestVideoTS) + *c.offset).Add(-100 * time.Millisecond)
	}
	since := decodedThrough
	if c.lastInput.After(since) {
		since = c.lastInput
	}
	c.mu.Unlock()
	c.Fresh(ctx, since, 3*time.Second)
	if err := ctx.Err(); err != nil {
		return mirroredFrame{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frame == nil {
		return mirroredFrame{}, errors.New("mirror disconnected before a frame was available")
	}
	return mirroredFrame{Image: c.frame, boardTS: c.captureTS, session: c.links,
		decodeBehind: !decodedThrough.IsZero() && !c.captured.After(decodedThrough)}, nil
}

func (s *Server) targetOnScreen(ctx context.Context, c *Conn, req Request) (image.Image, image.Point, error) {
	frame, err := s.currentFrame(ctx, c)
	if err != nil {
		return nil, image.Point{}, err
	}
	if freshness := frame.freshness(c); freshness.Status != "current" {
		return nil, image.Point{}, errors.New(freshness.Reason)
	}
	point, err := locateOnFrame(ctx, frame.Image, req)
	if err == nil {
		if freshness := frame.freshness(c); freshness.Status != "current" {
			return nil, image.Point{}, errors.New(freshness.Reason)
		}
	}
	if err == nil && !point.In(frame.Bounds()) {
		err = errors.New("resolved point falls outside the screenshot")
	}
	return frame.Image, point, err
}

// Check the surrounding control as well as the overall screen in case a label moved during OCR.
func targetChanged(before, after image.Image, pt image.Point) bool {
	if after == nil || before.Bounds() != after.Bounds() || pointer.Changed(before, after, .03) {
		return true
	}
	type cropper interface {
		SubImage(image.Rectangle) image.Image
	}
	a, okA := before.(cropper)
	b, okB := after.(cropper)
	if !okA || !okB {
		return false
	}
	radius := max(12, before.Bounds().Dx()/32)
	region := image.Rect(pt.X-radius, pt.Y-radius, pt.X+radius+1, pt.Y+radius+1).Intersect(before.Bounds())
	return pointer.Changed(a.SubImage(region), b.SubImage(region), .04)
}

func (s *Server) locatedTap(ctx context.Context, c *Conn, req Request) Response {
	frame, point, err := s.targetOnScreen(ctx, c, req)
	if err != nil {
		return fail(err)
	}
	if !req.Locate {
		report, err := s.preparePointer(ctx, c, firstPosition)
		if err != nil {
			return fail(err)
		}
		if report != nil {
			// Verification moves the pointer. Resolve the target again on the resulting screen.
			frame, point, err = s.targetOnScreen(ctx, c, req)
			if err != nil {
				return fail(err)
			}
		}
		if err := s.videoState(ctx, c); err != nil {
			return fail(err)
		}
		if targetChanged(frame, c.Frame(), point) {
			return fail(errors.New("screen changed during target lookup; take a fresh screenshot"))
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		s.recordShot(frame, frame.Bounds().Dx(), frame.Bounds().Dy())
		tap := Request{Cmd: "tap", Device: c.ID, Pts: []float64{float64(point.X), float64(point.Y)}, Aim: req.Aim, Count: req.Count}
		if !c.pointerChecked() {
			return fail(errors.New("screen or session changed during lookup; retry on the current screen"))
		}
		ctx = context.WithValue(ctx, inputSizeKey{}, frame.Bounds().Size())
		if r := s.io(ctx, c, tap); r.Status != "ok" {
			return r
		}
	}
	return ok(map[string]any{"x": point.X, "y": point.Y, "width": frame.Bounds().Dx(), "height": frame.Bounds().Dy(), "tapped": !req.Locate})
}
