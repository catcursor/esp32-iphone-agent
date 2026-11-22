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

func (s *Server) currentFrame(ctx context.Context, c *Conn) (image.Image, error) {
	if _, err := s.frame(ctx, c); err != nil {
		return nil, err
	}
	// A new connection replays buffered video starting at an older keyframe.
	// Let the decoder settle, then catch up to video already received from the board.
	c.Settled(ctx, 250*time.Millisecond, 250*time.Millisecond, 2*time.Second)
	c.mu.Lock()
	var received time.Time
	if n := len(c.gop); n > 0 && c.offset != nil && c.gop[n-1].TS != 0 {
		received = time.UnixMilli(int64(c.gop[n-1].TS) + *c.offset)
	}
	c.mu.Unlock()
	frame := c.Fresh(ctx, received.Add(-100*time.Millisecond), 3*time.Second)
	if frame == nil {
		return nil, errors.New("screen decoder has not caught up to the current phone frame")
	}
	return frame, nil
}

func (s *Server) targetOnScreen(ctx context.Context, c *Conn, req Request) (image.Image, image.Point, error) {
	frame, err := s.currentFrame(ctx, c)
	if err != nil {
		return nil, image.Point{}, err
	}
	point, err := locateOnFrame(ctx, frame, req)
	if err == nil && !point.In(frame.Bounds()) {
		err = errors.New("resolved point falls outside the screenshot")
	}
	return frame, point, err
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
