package pointer

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// VerificationTolerance is the per-axis tolerance used by the iOS automatic
// calibrator to decide a calibration needs redoing.
const VerificationTolerance = 0.015

var ErrVerification = errors.New("pointer accuracy verification failed")

type Probe struct {
	TargetX   float64 `json:"target_x"`
	TargetY   float64 `json:"target_y"`
	ObservedX float64 `json:"observed_x"`
	ObservedY float64 `json:"observed_y"`
	ErrorPx   float64 `json:"error_px"`
	Passed    bool    `json:"passed"`
}

type Verification struct {
	Verified   bool    `json:"verified"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Tolerance  float64 `json:"tolerance"`
	ToleranceX float64 `json:"tolerance_x_px"`
	ToleranceY float64 `json:"tolerance_y_px"`
	Error      float64 `json:"error"` // largest per-axis miss, as a fraction of that axis
	Samples    []Probe `json:"samples"`
	Span       struct {
		W int `json:"w"`
		H int `json:"h"`
	} `json:"span"`
}

// CheckAccuracy is independent of the calibration fit: move to held-out points
// in opposite quadrants, without aiming at or clicking them, and find the actual
// pointer using the mirrored video. Leave the saved fit untouched.
func (p *Pointer) CheckAccuracy(ctx context.Context) (Verification, error) {
	var r Verification
	if !p.Calibrated() {
		return r, ErrNotCalibrated
	}
	if p.screen == nil || !p.screen.Live() {
		return r, errors.New("pointer verification requires a live mirrored screen")
	}
	frame := p.screen.Frame()
	if frame == nil {
		return r, errors.New("pointer verification requires a mirrored frame")
	}
	p.Forget()
	r.Span.W, r.Span.H = p.Span.W, p.Span.H
	if p.Span.W < 100 || p.Span.W > 5000 || p.Span.H < 100 || p.Span.H > 5000 {
		return r, fmt.Errorf("%w: saved mouse span falls outside the supported calibration range", ErrVerification)
	}
	size := frame.Bounds().Size()
	r.Width, r.Height = size.X, size.Y
	w, h := float64(size.X), float64(size.Y)
	r.Tolerance = VerificationTolerance
	r.ToleranceX, r.ToleranceY = w*r.Tolerance, h*r.Tolerance
	for _, target := range [...]Point{{0.30, 0.65}, {0.70, 0.35}} {
		if err := p.Point(ctx, target.X, target.Y, false); err != nil {
			return r, err
		}
		found, ok, err := p.observe(ctx, Nudge, float64(Nudge)/float64(p.Span.W), Point{0.5, 0.5}, Point{0.5, 0.5})
		if err == nil && !ok && ctx.Err() == nil {
			// An idle mirror can deliver an older baseline and wake on the first
			// nudge. Give it one more round trip without moving to a new target.
			found, ok, err = p.observe(ctx, Nudge, float64(Nudge)/float64(p.Span.W), Point{0.5, 0.5}, Point{0.5, 0.5})
		}
		if err != nil {
			return r, err
		}
		if err := ctx.Err(); err != nil {
			return r, err
		}
		f := p.screen.Frame()
		if f == nil || f.Bounds().Size() != size {
			return r, errors.New("screen dimensions changed during pointer verification; retry on a stable screen")
		}
		if !ok {
			return r, fmt.Errorf("%w: pointer not visible at check point (%.0f, %.0f); keep AssistiveTouch enabled and the mirrored screen still", ErrVerification, target.X*w, target.Y*h)
		}
		dx, dy := found.X-target.X, found.Y-target.Y
		miss := math.Max(math.Abs(dx), math.Abs(dy))
		r.Error = max(r.Error, miss)
		r.Samples = append(r.Samples, Probe{target.X * w, target.Y * h, found.X * w, found.Y * h, math.Hypot(dx*w, dy*h), miss <= r.Tolerance})
		if miss > r.Tolerance {
			return r, fmt.Errorf("%w: target (%.0f, %.0f), observed (%.0f, %.0f); allowed %.1f px horizontally and %.1f px vertically", ErrVerification, target.X*w, target.Y*h, found.X*w, found.Y*h, r.ToleranceX, r.ToleranceY)
		}
	}
	r.Verified = true
	return r, nil
}

// Recalibrate measures a new fit privately and replaces the saved calibration
// only when the independent checks pass. Both operations move without clicking.
func (p *Pointer) Recalibrate(ctx context.Context) (Verification, error) {
	probe := *p
	probe.file = ""
	p.Forget()
	if err := probe.send(ctx, "release"); err != nil {
		return Verification{}, err
	}
	if err := probe.Calibrate(ctx); err != nil {
		return Verification{}, err
	}
	r, err := probe.CheckAccuracy(ctx)
	if err != nil {
		return r, err
	}
	probe.file, probe.Quality = p.file, &r.Error
	if err := probe.save(); err != nil {
		return r, err
	}
	*p = probe
	return r, nil
}
