// Package pointer turns screen positions into the board's relative mouse moves. Port of the iOS app's
// Pointer.swift (itself a port of phone.py).
//
// The pointer is relative, so we track where it is (in mouse units) and move from there; every few gestures it
// is referenced against the straight edges again to cancel drift. Coordinates are fractions of the screen
// (0..1, origin top-left), so they work for any frame size.
package pointer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	CalibrationMove = 200                    // mouse units moved diagonally for a one-point calibration
	CalibrationY    = 600                    // vertical travel for measuring the left-edge reference
	ReferenceEvery  = 10                     // gestures between edge references
	Hold            = 250 * time.Millisecond // rest before lifting, so iOS adds no momentum
	TapGap          = 150 * time.Millisecond // stay still after a click, so iOS sees a finished tap
	Nudge           = 12                     // mouse units the pointer is wiggled by to find it on screen
	AimTolerance    = 0.008                  // screen fraction; a bigger miss is corrected before pressing
	MaxMiss         = 0.06                   // bigger misses are still corrected, but not learned from
	SettleLag       = 300 * time.Millisecond // after a key, frames captured sooner still show the old screen
	ChangeWait      = 5 * time.Second        // how long GoHome watches for the screen to change
	// FreshWait: how long a wiggle waits for a frame captured after it. Over BLE, video yields to commands, and a
	// frame often took over a second: calibration with 1 s saw no pointer (2026-11-06).
	FreshWait = 3 * time.Second
)

// Device sends one board command; an "err" reply is an error. Links counts (re)connections.
type Device interface {
	Send(ctx context.Context, line string) error
	Links() int
}

// Screen is the mirrored screen.
type Screen interface {
	Live() bool
	Frame() image.Image
	// Settled waits until the image stopped changing for `quiet` (at least `least`, at most `most`).
	Settled(ctx context.Context, quiet, least, most time.Duration)
	// Fresh returns the first frame captured after `since`, nil if none within timeout.
	Fresh(ctx context.Context, since time.Time, timeout time.Duration) image.Image
}

type Span struct{ W, H int }

type saved struct {
	StrokeOffset [2]float64             `json:"stroke_offset_units"`
	W            int                    `json:"w"`
	H            int                    `json:"h"`
	Seen         map[string][][]float64 `json:"seen,omitempty"`
	Quality      *float64               `json:"quality,omitempty"`
	Aspect       *float64               `json:"aspect,omitempty"`
	Moves        string                 `json:"moves,omitempty"` // "axis": measured with moveXY's moves; older ones are redone
}

// MovesAxis marks a calibration measured with axis-by-axis moves.
const MovesAxis = "edges-segment-fit-v1"

type StrokeSample struct {
	Units  int     `json:"total_units"`
	Single float64 `json:"single_px"`
	Five   float64 `json:"five_px"`
	Offset float64 `json:"offset_px"`
}

type Pointer struct {
	StrokeSamples [2]StrokeSample
	dev           Device
	screen        Screen
	file          string
	Span          *Span // mouse units needed to cross the full screen width / height
	pos           *[2]float64
	strokeOffset  [2]float64 // extra travel per stroke, expressed in equivalent mouse units
	moves         int
	link          int
	// where the pointer really landed (seen on screen) for the fraction aimed at, per axis; a line fit through
	// them corrects every later move, so taps get better as the agent works
	seen    map[string][][2]float64
	Quality *float64 // pointer error at the last check, fraction of the screen
	Aspect  *float64 // frame width / height when calibrated
	Log     func(string)
	Sleep   func(context.Context, time.Duration) error
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// New loads a saved calibration from file, if any. screen may be nil.
func New(dev Device, screen Screen, file string) *Pointer {
	p := &Pointer{dev: dev, screen: screen, file: file, seen: map[string][][2]float64{"x": nil, "y": nil},
		Log: func(string) {}, Sleep: sleep}
	raw, err := os.ReadFile(file)
	var s saved
	if err != nil || json.Unmarshal(raw, &s) != nil || s.W <= 0 || s.H <= 0 || s.Moves != MovesAxis {
		return p
	}
	p.Span = &Span{s.W, s.H}
	p.strokeOffset = s.StrokeOffset
	p.Quality, p.Aspect = s.Quality, s.Aspect
	for _, axis := range []string{"x", "y"} {
		for _, v := range s.Seen[axis] {
			if len(v) == 2 {
				p.remember(axis, [2]float64{v[0], v[1]})
			}
		}
	}
	return p
}

func (p *Pointer) Calibrated() bool { return p.Span != nil }

// Forget makes the position unknown (someone else moved the pointer): the next gesture homes first.
func (p *Pointer) Forget() { p.pos = nil }

func (p *Pointer) remember(axis string, sample [2]float64) {
	s := append(p.seen[axis], sample)
	if len(s) > 12 { // deque(maxlen=12)
		s = s[len(s)-12:]
	}
	p.seen[axis] = s
}

// send runs one command; after a failed one the pointer position is unknown (the next gesture homes first).
func (p *Pointer) send(ctx context.Context, format string, a ...any) error {
	err := p.dev.Send(ctx, fmt.Sprintf(format, a...))
	if err != nil {
		p.pos = nil
	}
	return err
}

func (p *Pointer) screenAspect() float64 {
	if p.screen != nil {
		if f := p.screen.Frame(); f != nil && f.Bounds().Dy() > 0 {
			return float64(f.Bounds().Dx()) / float64(f.Bounds().Dy())
		}
	}
	return 9 / 19.5
}

// ErrNotFound: calibration could not see the pointer on screen.
var ErrNotFound = errors.New("calibration: pointer not found on screen (is the mouse pointer visible, AssistiveTouch on, Screen Mirroring on?)")

// ErrProbeFrame means a timestamped observation was unavailable. Measuring a
// new mouse calibration cannot fix a mirror that has not shown the move yet.
var ErrProbeFrame = errors.New("no fresh mirrored frame for pointer verification")

// referenceLeft ends at mouse coordinates (0, yUnits). Finding the
// top with X inside the screen keeps rounded corners out of the measurement.
func (p *Pointer) referenceLeft(ctx context.Context, yUnits int) error {
	if err := p.referenceTop(ctx); err != nil {
		return err
	}
	if err := p.rawStroke(ctx, 0, yUnits); err != nil {
		return err
	}
	if err := p.edgeStroke(ctx, -5000, 0); err != nil {
		return err
	}
	p.pos, p.moves = &[2]float64{0, float64(yUnits) + p.strokeOffset[1]}, 0
	return nil
}

func (p *Pointer) referenceTop(ctx context.Context) error {
	p.Forget()
	if err := p.edgeStroke(ctx, -5000, 0); err != nil {
		return err
	}
	if err := p.rawStroke(ctx, CalibrationMove, 0); err != nil {
		return err
	}
	return p.edgeStroke(ctx, 0, -5000)
}

func (p *Pointer) edgeStroke(ctx context.Context, x, y int) error {
	if err := p.send(ctx, "move %d %d 40", x, y); err != nil {
		return err
	}
	return p.Sleep(ctx, Hold)
}

func (p *Pointer) rawStroke(ctx context.Context, x, y int) error {
	if err := p.send(ctx, "move %d %d", x, y); err != nil {
		return err
	}
	return p.Sleep(ctx, Hold)
}

func (p *Pointer) edgeFrame(ctx context.Context, since time.Time) image.Image {
	if p.screen.Fresh(ctx, since, FreshWait) == nil {
		return nil
	}
	p.screen.Settled(ctx, 400*time.Millisecond, 800*time.Millisecond, 3*time.Second)
	return p.screen.Frame()
}

// sampleEdges measures the same pair of straight-edge positions with either
// one raw stroke per axis or five, resetting the physical origin each time.
func (p *Pointer) sampleEdges(ctx context.Context, xUnits, yUnits, parts int) (Point, Point, error) {
	var zero Point
	if err := p.referenceTop(ctx); err != nil {
		return zero, zero, err
	}
	for i := 0; i < parts; i++ {
		if err := p.rawStroke(ctx, 0, yUnits/parts); err != nil {
			return zero, zero, err
		}
	}
	since := time.Now()
	if err := p.edgeStroke(ctx, -5000, 0); err != nil {
		return zero, zero, err
	}
	left := p.edgeFrame(ctx, since)
	if left == nil {
		return zero, zero, ErrProbeFrame
	}
	for i := 0; i < parts; i++ {
		if err := p.rawStroke(ctx, xUnits/parts, 0); err != nil {
			return zero, zero, err
		}
	}
	since = time.Now()
	if err := p.edgeStroke(ctx, 0, -5000); err != nil {
		return zero, zero, err
	}
	top := p.edgeFrame(ctx, since)
	if top == nil || left.Bounds() != top.Bounds() {
		return zero, zero, ErrProbeFrame
	}
	a, b := ToGray(left), ToGray(top)
	l, okL := edgeCenter(a, b, false)
	t, okT := edgeCenter(b, a, true)
	if !okL || !okT || t.X <= l.X || l.Y <= t.Y {
		return zero, zero, ErrNotFound
	}
	return l, t, nil
}

// For a distance d sent in n equal strokes: travel = gain*d + n*offset.
// Both samples are normalised to the screen size before fitting.
func fitStrokes(units int, one, five float64) (span int, offset float64, err error) {
	extra := (five - one) / 4
	gain := (one - extra) / float64(units)
	if gain < 1./5000 || gain > 1./100 || math.Abs(extra) >= gain*float64(units)/5 {
		return 0, 0, ErrNotFound
	}
	return int(math.Round(1 / gain)), extra / gain, nil
}

func (p *Pointer) Calibrate(ctx context.Context) error {
	if p.screen == nil || !p.screen.Live() {
		return ErrNotFound
	}
	defer p.Forget()
	// Shorten the horizontal stroke on screens where 400 reaches the corner.
	var l, t, l5, t5 Point
	var err error
	var xUnits int
	for _, xUnits = range []int{400, CalibrationMove} {
		l, t, err = p.sampleEdges(ctx, xUnits, CalibrationY, 1)
		if err == nil {
			l5, t5, err = p.sampleEdges(ctx, xUnits, CalibrationY, 5)
		}
		if !errors.Is(err, ErrNotFound) {
			break
		}
	}
	if err != nil {
		return err
	}
	w, bx, err := fitStrokes(xUnits, t.X-l.X, t5.X-l5.X)
	if err != nil {
		return err
	}
	h, by, err := fitStrokes(CalibrationY, l.Y-t.Y, l5.Y-t5.Y)
	if err != nil {
		return err
	}
	frame := p.screen.Frame()
	if frame == nil {
		return ErrProbeFrame
	}
	sz := frame.Bounds().Size()
	sample := func(units int, one, five float64, pixels int) StrokeSample {
		return StrokeSample{Units: units, Single: one * float64(pixels), Five: five * float64(pixels), Offset: (five - one) * float64(pixels) / 4}
	}
	p.StrokeSamples = [2]StrokeSample{sample(xUnits, t.X-l.X, t5.X-l5.X, sz.X), sample(CalibrationY, l.Y-t.Y, l5.Y-t5.Y, sz.Y)}
	aspect := p.screenAspect()
	p.Span, p.Aspect, p.Quality = &Span{w, h}, &aspect, nil
	p.strokeOffset = [2]float64{bx, by}
	// Fix handles the physical position of the straight edge. The per-stroke
	// offset belongs to each relative command, not to the absolute target.
	p.seen = map[string][][2]float64{"x": {{0, l.X}, {1, 1 + l.X}}, "y": {{0, t.Y}, {1, 1 + t.Y}}}
	return p.save()
}

// Observe the outward nudge and its return. The return frame distinguishes the
// pointer from an animation that happened elsewhere while the pointer moved.
func (p *Pointer) observe(ctx context.Context, nudge int, shift float64, near, reach Point) (Point, bool, error) {
	// iOS glides the pointer to a new spot over ~1 s, and the first frames arrive ~0.2 s late: a shorter wait took
	// a frame from before the stop's move as "before" (2026-11-06).
	p.screen.Settled(ctx, 400*time.Millisecond, 800*time.Millisecond, 3*time.Second)
	before, sent := p.screen.Frame(), time.Now()
	if before == nil {
		return Point{}, false, fmt.Errorf("%w before moving; wait for the live mirror and retry", ErrProbeFrame)
	}
	if err := p.send(ctx, "move %d 0", nudge); err != nil {
		return Point{}, false, err
	}
	after := p.screen.Fresh(ctx, sent, FreshWait)
	if after != nil {
		p.screen.Settled(ctx, 150*time.Millisecond, 100*time.Millisecond, 600*time.Millisecond)
		after = p.screen.Frame()
	}
	sent = time.Now()
	if err := p.send(ctx, "move %d 0", -nudge); err != nil {
		return Point{}, false, err
	}
	if after == nil {
		return Point{}, false, fmt.Errorf("%w after the outward movement within %s; wait for the live mirror to catch up and retry", ErrProbeFrame, FreshWait)
	}
	if p.screen.Fresh(ctx, sent, FreshWait) == nil {
		if err := ctx.Err(); err != nil {
			return Point{}, false, err
		}
		return Point{}, false, fmt.Errorf("%w after the return movement within %s; wait for the live mirror to catch up and retry", ErrProbeFrame, FreshWait)
	}
	p.screen.Settled(ctx, 150*time.Millisecond, 100*time.Millisecond, 600*time.Millisecond)
	back := p.screen.Frame()
	if back == nil || back.Bounds() != before.Bounds() || after.Bounds() != before.Bounds() {
		return Point{}, false, fmt.Errorf("%w: display changed during observation; retry on a stable screen", ErrProbeFrame)
	}
	pt, ok := LocateBack(before, after, back, near, shift, reach)
	if !ok && reach.X < 0.5 {
		pt, ok = LocateBack(before, after, back, Point{0.5, 0.5}, shift, Point{0.5, 0.5})
	}
	return pt, ok, nil
}

// FinishCalibration: x, y is where the pointer ended up after `home` + `move 200 200` (one point).
func (p *Pointer) FinishCalibration(x, y float64) error {
	aspect := p.screenAspect()
	p.Quality, p.Aspect = nil, &aspect
	p.Span = &Span{int(math.RoundToEven(CalibrationMove / x)), int(math.RoundToEven(CalibrationMove / y))}
	p.seen = map[string][][2]float64{"x": nil, "y": nil}
	return p.save()
}

// save keeps the calibration plus the observed landings, so the correction survives a restart.
func (p *Pointer) save() error {
	if p.Span == nil || p.file == "" {
		return nil
	}
	s := saved{StrokeOffset: p.strokeOffset, W: p.Span.W, H: p.Span.H, Seen: map[string][][]float64{}, Quality: p.Quality, Aspect: p.Aspect, Moves: MovesAxis}
	for axis, pts := range p.seen {
		s.Seen[axis] = [][]float64{}
		for _, v := range pts {
			s.Seen[axis] = append(s.Seen[axis], []float64{v[0], v[1]})
		}
	}
	f, err := os.CreateTemp(filepath.Dir(p.file), ".calibration-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := errors.Join(json.NewEncoder(f).Encode(s), f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), p.file)
}

// ErrNotCalibrated: run Calibrate first.
var ErrNotCalibrated = errors.New("pointer not calibrated")

// Point moves the pointer to x, y. aim: find it on screen afterwards and fix a miss (slower).
func (p *Pointer) Point(ctx context.Context, x, y float64, aim bool) error {
	if !p.Calibrated() {
		return ErrNotCalibrated
	}
	if err := p.moveTo(ctx, p.dx(x), p.dy(y), true); err != nil {
		return err
	}
	if aim {
		return p.aim(ctx, x, y)
	}
	return nil
}

// aim is the closed loop: find the real pointer on screen, fix a miss, and learn the error for later moves.
func (p *Pointer) aim(ctx context.Context, x, y float64) error {
	found, ok, err := p.measure(ctx, x, y)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("aim: pointer not visible at (%.3f, %.3f); keep the mirrored screen still and retry", x, y)
	}
	if math.Max(math.Abs(found.X-x), math.Abs(found.Y-y)) <= MaxMiss { // learn from normal drift only
		p.remember("x", [2]float64{p.Fix(x, "x"), found.X})
		p.remember("y", [2]float64{p.Fix(y, "y"), found.Y})
		p.save()
	}
	if math.Abs(found.X-x) <= AimTolerance && math.Abs(found.Y-y) <= AimTolerance {
		return nil
	}
	mx := int(math.RoundToEven((x - found.X) * float64(p.Span.W)))
	my := int(math.RoundToEven((y - found.Y) * float64(p.Span.H)))
	moved, err := p.moveCalibrated(ctx, float64(mx), float64(my))
	if err != nil { // fix this miss right away
		return err
	}
	if p.pos != nil {
		p.pos = &[2]float64{p.pos[0] + moved[0], p.pos[1] + moved[1]}
	}
	found, ok, err = p.measure(ctx, x, y)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("aim: pointer not visible after correction")
	}
	if math.Abs(found.X-x) > AimTolerance || math.Abs(found.Y-y) > AimTolerance {
		return fmt.Errorf("aim: pointer remained outside tolerance after correction (wanted %.3f, %.3f; observed %.3f, %.3f)", x, y, found.X, found.Y)
	}
	return nil
}

// measure wiggles the pointer and finds it on screen, near the target first, then anywhere.
func (p *Pointer) measure(ctx context.Context, x, y float64) (Point, bool, error) {
	if p.screen == nil || p.Span == nil {
		return Point{}, false, nil
	}
	nudge := Nudge
	if x >= 0.9 { // stay clear of the screen edge
		nudge = -Nudge
	}
	p.screen.Settled(ctx, 150*time.Millisecond, 100*time.Millisecond, 600*time.Millisecond) // pointer at rest
	before, sent := p.screen.Frame(), time.Now()
	if err := p.send(ctx, "move %d 0", nudge); err != nil {
		return Point{}, false, err
	}
	after := p.screen.Fresh(ctx, sent, 1200*time.Millisecond)
	if after != nil {
		p.screen.Settled(ctx, 100*time.Millisecond, 50*time.Millisecond, 400*time.Millisecond)
		after = p.screen.Frame()
	}
	if err := p.send(ctx, "move %d 0", -nudge); err != nil {
		return Point{}, false, err
	}
	shift := float64(nudge) / float64(p.Span.W)
	if pt, ok := Locate(before, after, Point{x, y}, shift, Point{0.12, 0.08}); ok {
		return pt, true, nil
	}
	pt, ok := Locate(before, after, Point{0.5, 0.5}, shift, Point{0.5, 0.5})
	return pt, ok, nil
}

// Drift is how far the learned correction has moved targets away from the plain calibration.
func (p *Pointer) Drift() float64 {
	d := 0.0
	for _, t := range []float64{0.2, 0.8} {
		for _, axis := range []string{"x", "y"} {
			d = math.Max(d, math.Abs(p.Fix(t, axis)-t))
		}
	}
	return d
}

// Fix is the fraction to aim at so the pointer lands on t, from the line through the observed landings.
func (p *Pointer) Fix(t float64, axis string) float64 {
	pts := p.seen[axis]
	if len(pts) < 2 {
		return t
	}
	n := float64(len(pts))
	mc, mf := 0.0, 0.0
	for _, v := range pts {
		mc += v[0] / n
		mf += v[1] / n
	}
	variance, cov := 0.0, 0.0
	for _, v := range pts {
		variance += (v[0] - mc) * (v[0] - mc)
		cov += (v[0] - mc) * (v[1] - mf)
	}
	a := 1.0 // landed = a*aimed + b
	if variance > 1e-4 {
		a = cov / variance
	}
	if a <= 0.85 || a >= 1.15 {
		a = 1
	}
	b := mf - a*mc
	return (t - b) / a
}

func (p *Pointer) moveTo(ctx context.Context, tx, ty int, rehome bool) error {
	if l := p.dev.Links(); p.link != l { // new link: a dropped gesture may have left the button down
		if err := p.send(ctx, "release"); err != nil {
			return err
		}
		p.pos, p.link = nil, l
	}
	if p.pos == nil || rehome && p.moves >= ReferenceEvery {
		if err := p.referenceLeft(ctx, CalibrationMove); err != nil {
			return err
		}
	}
	x, y := p.pos[0], p.pos[1]
	p.pos = nil // unknown until the move succeeds
	moved, err := p.moveCalibrated(ctx, float64(tx)-x, float64(ty)-y)
	if err != nil {
		return err
	}
	p.pos, p.moves = &[2]float64{x + moved[0], y + moved[1]}, p.moves+1
	return nil
}

// Choose the nearest reachable displacement, including the option of staying
// still for tiny deltas. Track what was sent so rounding cannot accumulate.
func strokeCommand(delta, offset float64) (int, float64) {
	if delta == 0 {
		return 0, 0
	}
	units := max(1, int(math.Round(math.Abs(delta)-offset)))
	travel := float64(units) + offset
	if travel <= 0 || math.Abs(travel-math.Abs(delta)) >= math.Abs(delta) {
		return 0, 0
	}
	if delta < 0 {
		return -units, -travel
	}
	return units, travel
}

func (p *Pointer) moveCalibrated(ctx context.Context, dx, dy float64) ([2]float64, error) {
	x, sx := strokeCommand(dx, p.strokeOffset[0])
	y, sy := strokeCommand(dy, p.strokeOffset[1])
	return [2]float64{sx, sy}, p.moveXY(ctx, x, y)
}

// moveXY moves the pointer along x, then along y. The board paces a move in reports of at most 10 units per axis,
// and iOS accelerates by a report's length: a diagonal report (10, 10) went 40% further per unit than (10, 0), so
// how far one axis went depended on the other, and taps after a diagonal calibration missed (2026-11-06).
// One axis at a time keeps every report the same length: the units per screen stay the same in any direction.
func (p *Pointer) moveXY(ctx context.Context, dx, dy int) error {
	if dx != 0 {
		if err := p.send(ctx, "move %d 0", dx); err != nil {
			return err
		}
	}
	if dy != 0 {
		return p.send(ctx, "move 0 %d", dy)
	}
	return nil
}

// Reset lets go of the button and re-homes the pointer, whatever state a gesture left.
func (p *Pointer) Reset(ctx context.Context) error {
	p.pos = nil
	if err := p.send(ctx, "release"); err != nil {
		return err
	}
	return p.referenceLeft(ctx, CalibrationMove)
}

// Tap clicks count times (at least once) at x, y without moving in between: 2 is a double tap.
func (p *Pointer) Tap(ctx context.Context, x, y float64, aim bool, count int) error {
	if err := p.Point(ctx, x, y, aim); err != nil {
		return err
	}
	for i := 0; i < max(count, 1); i++ {
		if err := p.send(ctx, "click"); err != nil {
			return err
		}
	}
	return p.Sleep(ctx, TapGap)
}

// LongPress presses at x, y for d without moving.
func (p *Pointer) LongPress(ctx context.Context, x, y float64, d time.Duration) error {
	if err := p.Point(ctx, x, y, false); err != nil {
		return err
	}
	if err := p.send(ctx, "press"); err != nil {
		return err
	}
	err := p.Sleep(ctx, d)
	if rerr := p.send(context.WithoutCancel(ctx), "release"); err == nil {
		err = rerr
	}
	return err
}

func (p *Pointer) Swipe(ctx context.Context, x1, y1, x2, y2 float64, hold bool) error {
	return p.Drag(ctx, []Point{{x1, y1}, {x2, y2}}, hold)
}

// Drag presses at the first point, moves through the rest, releases: one continuous stroke. hold rests at the
// end first, so lists and pickers stop exactly there; system gestures (home, back) need the flick.
func (p *Pointer) Drag(ctx context.Context, pts []Point, hold bool) error {
	if err := p.Point(ctx, pts[0].X, pts[0].Y, false); err != nil {
		return err
	}
	if err := p.send(ctx, "press"); err != nil {
		return err
	}
	err := func() error {
		for _, pt := range pts[1:] {
			if err := p.moveTo(ctx, p.dx(pt.X), p.dy(pt.Y), false); err != nil { // never home mid-drag
				return err
			}
		}
		if hold {
			return p.Sleep(ctx, Hold)
		}
		return nil
	}()
	if rerr := p.send(context.WithoutCancel(ctx), "release"); err == nil {
		err = rerr
	}
	return err
}

// Type types text as the board can (see Typeable).
func (p *Pointer) Type(ctx context.Context, text string) (string, error) {
	ascii := Typeable(text)
	if ascii == "" {
		return "", errors.New("nothing typeable in that text (the board types ASCII only)")
	}
	// Spaces go as their own key: inside a `type` line iOS dropped every one ("coffee near East Broadway" came
	// out "coffeenearEastBroadway" in Maps, 2026-10-21), while a separate space key always landed.
	for i, word := range strings.Split(ascii, " ") {
		if i > 0 {
			if err := p.Key(ctx, "space"); err != nil {
				return ascii, err
			}
		}
		if word == "" {
			continue
		}
		if err := p.send(ctx, "type %s", word); err != nil {
			return ascii, err
		}
	}
	return ascii, nil
}

func (p *Pointer) Key(ctx context.Context, combo string) error { return p.send(ctx, "key %s", combo) }

// GoHome goes to the Home Screen like the app's leaveApp: the consumer-control Home key, then Cmd+H, then the flick
// up from the home bar, each until the screen really changes. The board acknowledges a key iOS ignores (seen on
// an iPhone in a video app, 2026-10-21), so an "ok" alone proves nothing; without video the key's "ok" is all there is.
func (p *Pointer) GoHome(ctx context.Context, prepareSwipe func() error) error {
	tries := []func() error{
		func() error { return p.send(ctx, "homescreen") },
		func() error { return p.Key(ctx, "cmd+h") },
		func() error {
			if prepareSwipe != nil {
				if err := prepareSwipe(); err != nil {
					return err
				}
			} else if !p.Calibrated() {
				if err := p.Calibrate(ctx); err != nil {
					return err
				}
			}
			return p.HomeSwipe(ctx)
		},
	}
	var last error
	for i, try := range tries {
		before, sent := p.frame(), time.Now()
		err := try()
		if err != nil && strings.HasPrefix(err.Error(), "busy") {
			return err
		}
		if err == nil && before == nil && i != 1 {
			return nil // no video to check against: trust the Home key, and after Cmd+H still flick (as before)
		}
		if err == nil && before != nil && p.changedSince(ctx, before, sent) {
			return nil
		}
		last = err
	}
	if last != nil {
		return last
	}
	return errors.New("the Home Screen did not come up (Home key, Cmd+H and the swipe up all left the screen as it was)")
}

func (p *Pointer) frame() image.Image {
	if p.screen == nil || !p.screen.Live() {
		return nil
	}
	return p.screen.Frame()
}

// changedSince: a frame captured at least SettleLag after `sent` differs from before in more than a sliver (the
// pointer alone is not it). Frames from the first SettleLag still show the old screen, and over BLE the picture
// lags the phone and the Home Screen animation starts late: one look after 1.5 s, and later a 3 s window, missed
// it and the next fallback ran on top (2026-10-21). So: board capture time >= sent+SettleLag, for up to ChangeWait.
func (p *Pointer) changedSince(ctx context.Context, before image.Image, sent time.Time) bool {
	deadline := sent.Add(ChangeWait)
	for since := sent.Add(SettleLag); time.Now().Before(deadline); since = later(since, time.Now()) {
		f := p.screen.Fresh(ctx, since, time.Until(deadline))
		if f == nil {
			return false
		}
		if Changed(before, f, 0.05) {
			return true
		}
	}
	return false
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// HomeSwipe is a real home-bar flick (a flick, not a rest: resting opens the app switcher).
func (p *Pointer) HomeSwipe(ctx context.Context) error {
	defer func() { p.pos = nil }() // edge clamping invalidates the tracked position
	return p.Swipe(ctx, 0.5, 0.995, 0.5, 0.15, false)
}

func (p *Pointer) dx(x float64) int {
	return int(math.RoundToEven(math.Min(math.Max(p.Fix(x, "x"), 0), 1) * float64(p.Span.W)))
}

func (p *Pointer) dy(y float64) int {
	return int(math.RoundToEven(math.Min(math.Max(p.Fix(y, "y"), 0), 1) * float64(p.Span.H)))
}
