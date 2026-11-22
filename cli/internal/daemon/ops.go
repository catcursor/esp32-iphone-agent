package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/config"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/pointer"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/textloc"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/video"
	"golang.org/x/image/draw"
)

var (
	errMirroring = errors.New("screen mirroring is off: on the iPhone open Control Center > Screen Mirroring > ESP32 Agent")
	errFrozen    = errors.New("video frozen: restart Screen Mirroring on the iPhone (Control Center > Screen Mirroring > Stop Mirroring, then ESP32 Agent)")
)

func num(v any) float64 { f, _ := v.(float64); return f }

func (c *Conn) waitForStatus(ctx context.Context) error {
	for i := 0; i < 30; i++ { // the first status arrives within a second of connecting
		if _, age := c.Status(); age >= 0 {
			break
		}
		if sleepCtx(ctx, 100*time.Millisecond) != nil {
			return ctx.Err()
		}
	}
	return nil
}

// videoState explains why there is no usable screen, nil when mirroring streams.
func (s *Server) videoState(ctx context.Context, c *Conn) error {
	if err := c.waitForStatus(ctx); err != nil {
		return err
	}
	st, _ := c.Status()
	if num(st["fz"]) == 1 {
		return errFrozen
	}
	if st["ap"] != "streaming" {
		return errMirroring
	}
	c.mu.Lock()
	ff := c.ffmpeg
	c.mu.Unlock()
	if ff == "" {
		_, err := video.FFmpeg()
		if err == nil {
			err = errors.New("ffmpeg could not decode the video (see daemon.log)")
		}
		return err
	}
	return nil
}

// frame returns the screen as it is after the last action (like the app's VideoFeed.afterAction).
func (s *Server) frame(ctx context.Context, c *Conn) (image.Image, error) {
	if err := s.videoState(ctx, c); err != nil {
		return nil, err
	}
	if c.Frame() == nil && c.Fresh(ctx, time.Time{}, 6*time.Second) == nil {
		// nothing streamed yet (the stream starts at the next keyframe): ask for the current GOP
		sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := c.Send(sctx, "snap")
		cancel()
		if err != nil {
			return nil, fmt.Errorf("no video from the board: %w", err)
		}
		if c.Fresh(ctx, time.Time{}, 5*time.Second) == nil {
			return nil, errors.New("no video from the board yet; try again")
		}
	}
	c.mu.Lock()
	acted, shot := c.lastInput, c.lastShot
	c.mu.Unlock()
	if acted.After(shot) {
		c.Settled(ctx, 500*time.Millisecond, 300*time.Millisecond, 3*time.Second)
	}
	c.mu.Lock()
	c.lastShot = time.Now()
	c.mu.Unlock()
	if st, _ := c.Status(); num(st["fz"]) == 1 {
		return nil, errFrozen
	}
	return c.Frame(), nil
}

func encode(img image.Image, format string, quality int, scale float64, maxSize int) ([]byte, image.Rectangle, error) {
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	if scale <= 0 || scale > 1 {
		scale = 1
	}
	if maxSize > 0 && max(w, h)*scale > float64(maxSize) {
		scale = float64(maxSize) / max(w, h)
	}
	if scale < 1 {
		dst := image.NewRGBA(image.Rect(0, 0, max(1, int(w*scale+0.5)), max(1, int(h*scale+0.5))))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "", "png":
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	case "jpeg", "jpg":
		if quality <= 0 || quality > 100 {
			quality = 90
		}
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	default:
		err = fmt.Errorf("format must be png or jpeg")
	}
	return buf.Bytes(), img.Bounds(), err
}

func (s *Server) screenshot(ctx context.Context, c *Conn, req Request) Response {
	frame, err := s.currentFrame(ctx, c)
	if err != nil {
		return fail(err)
	}
	img := frame.Image
	data, r, err := encode(img, req.Format, req.Quality, req.Scale, req.MaxSize)
	if err != nil {
		return fail(err)
	}
	format := "png"
	if req.Format == "jpeg" || req.Format == "jpg" {
		format = "jpeg"
	}
	s.recordShot(img, r.Dx(), r.Dy())
	size := img.Bounds().Size()
	source := image.Rectangle{Max: size}
	ocr := textloc.Scan(ctx, data, r.Size(), source)
	return ok(Screenshot{Format: format, Data: base64.StdEncoding.EncodeToString(data), Device: c.ID,
		Width: r.Dx(), Height: r.Dy(), SourceWidth: size.X, SourceHeight: size.Y,
		SourceRect: textloc.Box{Width: size.X, Height: size.Y}, Freshness: frame.freshness(c), OCR: ocr})
}

func (c *Conn) pointer(dir string) *pointer.Pointer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil {
		c.ptr = pointer.New(device{c}, c, config.CalibrationPath(dir, c.ID))
	}
	return c.ptr
}

// size is the screenshot size coordinates refer to: the latest frame, else the AirPlay resolution.
func (c *Conn) size() (float64, float64, error) {
	if f := c.Frame(); f != nil {
		return float64(f.Bounds().Dx()), float64(f.Bounds().Dy()), nil
	}
	st, _ := c.Status()
	if w, h := num(st["w"]), num(st["h"]); w > 0 && h > 0 {
		return w, h, nil
	}
	return 0, 0, errors.New("screen size unknown (no video yet): take a screenshot first")
}

type calibrationMode int

const (
	firstPosition calibrationMode = iota
	verifyCalibration
	newCalibration
)

func (c *Conn) pointerChecked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess != nil && c.checkedLink == c.links && c.frame != nil && c.checkedSize == c.frame.Bounds().Size()
}

// Input commands hold inputMu. Never reuse a successful check for another link
// or display size, even when that new session loaded the same calibration file.
func (s *Server) preparePointer(ctx context.Context, c *Conn, mode calibrationMode) (*pointer.Verification, error) {
	if mode == firstPosition && c.pointerChecked() {
		return nil, nil
	}
	c.mu.Lock()
	c.checkedSize = image.Point{}
	c.mu.Unlock()
	frame, err := s.currentFrame(ctx, c)
	if err != nil {
		return nil, err
	}
	if frame.decodeBehind {
		return nil, errors.New("screen decoder has not caught up to the current phone frame")
	}
	link := c.Links()
	p := c.pointer(s.Dir)
	var report pointer.Verification
	if mode == newCalibration || mode == firstPosition && !p.Calibrated() {
		report, err = p.Recalibrate(ctx)
	} else {
		report, err = p.CheckAccuracy(ctx)
		if mode == firstPosition && errors.Is(err, pointer.ErrVerification) && ctx.Err() == nil {
			// Only a measured miss or an invisible pointer warrants one new fit.
			var recalibrationErr error
			if report, recalibrationErr = p.Recalibrate(ctx); recalibrationErr != nil {
				err = fmt.Errorf("%v; one recalibration attempt failed: %w", err, recalibrationErr)
			} else {
				err = nil
			}
		}
	}
	if err != nil {
		return &report, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	size := image.Pt(report.Width, report.Height)
	if c.sess == nil || c.links != link || c.frame == nil || c.frame.Bounds().Size() != size || c.status["ap"] != "streaming" || num(c.status["fz"]) == 1 || c.status["owner"] == "app" {
		report.Verified = false
		return &report, errors.New("screen or session changed during pointer verification; retry on the current screen")
	}
	c.checkedLink, c.checkedSize = link, size
	return &report, nil
}

func (s *Server) calibrate(ctx context.Context, c *Conn, mode calibrationMode) Response {
	report, err := s.preparePointer(ctx, c, mode)
	if err != nil {
		r := fail(err)
		if report != nil && report.Width > 0 {
			r.Data = report
		}
		return r
	}
	return ok(report)
}

// textChunk keeps one `type` line well inside a single BLE write.
const textChunk = 160

func (s *Server) io(ctx context.Context, c *Conn, req Request) Response {
	p := c.pointer(s.Dir)
	err := func() error {
		switch req.Cmd {
		case "text":
			typed, err := "", error(nil)
			for rest := pointer.Typeable(req.Text); rest != "" && err == nil; {
				n := min(len(rest), textChunk)
				for n > 0 && !utf8.RuneStart(rest[n-1]) {
					n--
				}
				_, err = p.Type(ctx, rest[:n])
				typed, rest = typed+rest[:n], rest[n:]
			}
			if typed == "" && err == nil {
				err = errors.New("nothing typeable in that text (the board types ASCII only)")
			}
			return err
		case "keys":
			return p.Key(ctx, strings.ToLower(req.Text))
		case "button":
			if !strings.EqualFold(req.Text, "HOME") {
				return fmt.Errorf("button %s not supported on ESP32 Agent (only HOME)", req.Text)
			}
			return p.GoHome(ctx, func() error {
				_, err := s.preparePointer(ctx, c, firstPosition)
				return err
			})
		}
		expected, located := ctx.Value(inputSizeKey{}).(image.Point)
		if !located {
			if _, err := s.preparePointer(ctx, c, firstPosition); err != nil {
				return err
			}
		}
		w, h, err := c.size()
		if err != nil {
			return err
		}
		size := image.Pt(int(w), int(h))
		if located && expected != size {
			return errors.New("screen dimensions changed after target lookup; retry on the current screen")
		}
		ctx = context.WithValue(ctx, inputSizeKey{}, size)
		pt := func(i int) (float64, float64) { return req.Pts[i] / w, req.Pts[i+1] / h }
		switch req.Cmd {
		case "tap":
			x, y := pt(0)
			return p.Tap(ctx, x, y, req.Aim, req.Count)
		case "longpress":
			x, y := pt(0)
			d := time.Duration(req.Ms) * time.Millisecond
			if d <= 0 {
				d = time.Second
			}
			return p.LongPress(ctx, x, y, d)
		case "swipe":
			x1, y1 := pt(0)
			x2, y2 := pt(2)
			return p.Swipe(ctx, x1, y1, x2, y2, true)
		}
		return nil
	}()
	s.recordAction(c, req, err)
	if err != nil {
		return fail(err)
	}
	out := map[string]string{"message": req.Cmd + " done"}
	if req.Cmd == "text" && pointer.HasHan(req.Text) {
		out["typed_as"] = pointer.Typeable(req.Text)
		out["note"] = "needs the iPhone's Chinese (pinyin) keyboard; if letters appear, tap the globe key and type again"
	}
	return ok(out)
}

// screencapture streams raw H.264 (Annex-B, from the next keyframe) or MJPEG, after one JSON header line.
func (s *Server) screencapture(ctx context.Context, req Request, w io.Writer) {
	enc := json.NewEncoder(w)
	c, err := s.conn(ctx, req.Device)
	if err == nil {
		err = s.videoState(ctx, c)
	}
	if err == nil && req.Format != "h264" && req.Format != "mjpeg" {
		err = errors.New("format must be h264 or mjpeg")
	}
	if err != nil {
		enc.Encode(fail(err))
		return
	}
	enc.Encode(ok(map[string]string{"format": req.Format}))
	if req.Format == "h264" {
		ch := make(chan video.AU, 256)
		c.mu.Lock()
		c.subs[ch] = true
		config, gop := c.config, append([]video.AU{}, c.gop...)
		c.mu.Unlock()
		defer func() { c.mu.Lock(); delete(c.subs, ch); c.mu.Unlock() }()
		started := len(gop) > 0 && config != nil
		if started {
			w.Write(config)
			for _, au := range gop {
				w.Write(au.Data)
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case au := <-ch:
				if !started && au.Flags&(video.IDR|video.Config) == 0 {
					continue
				}
				if !started && au.Flags&video.Config == 0 && config != nil {
					w.Write(config)
				}
				started = true
				if _, err := w.Write(au.Data); err != nil {
					return
				}
			}
		}
	}
	ch := make(chan image.Image, 4)
	c.mu.Lock()
	c.frameSubs[ch] = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.frameSubs, ch); c.mu.Unlock() }()
	for {
		select {
		case <-ctx.Done():
			return
		case img := <-ch:
			data, _, _ := encode(img, "jpeg", req.Quality, 1, 0)
			hdr := fmt.Sprintf("--BoundaryString\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(data))
			if _, err := w.Write(append(append([]byte(hdr), data...), "\r\n"...)); err != nil {
				return
			}
		}
	}
}
