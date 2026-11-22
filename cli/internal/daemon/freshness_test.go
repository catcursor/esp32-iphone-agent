package daemon

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/video"
)

func TestInputBoundaryIgnoresProbesAndTracksFinalRelease(t *testing.T) {
	base := time.Now()
	c := &Conn{}
	for _, cmd := range []string{"release", "home", "move 12 0", "move -12 0", "snap"} {
		c.noteInput(cmd, base)
	}
	if !c.lastInput.IsZero() {
		t.Fatal("pointer verification marked the application screen stale")
	}
	for i, cmd := range []string{"click", "click", "key enter", "type text", "homescreen", "press", "release"} {
		issued := base.Add(time.Duration(i) * time.Second)
		c.noteInput(cmd, issued)
		if c.lastInput != issued {
			t.Fatalf("lost boundary for %q", cmd)
		}
	}
	c.noteInput("release", base.Add(time.Hour))
	if c.lastInput != base.Add(6*time.Second) {
		t.Fatal("a pointer reset replaced the final drag release time")
	}
}

func TestReplayedVideoMustCatchUpToBoardWatermark(t *testing.T) {
	c := newConn("test", "test", "simulated")
	img := image.NewRGBA(image.Rect(0, 0, 100, 150))
	c.via = "wifi"
	c.onStatus([]byte(`{"ap":"streaming","up":600000,"vage":100}`))
	// The decoder has already caught up with this computer's most recent AU,
	// but the board has captured almost seven more minutes of the phone.
	c.latestVideoTS = 180000
	c.onFrame(video.Frame{Img: img, TS: 180000})
	f, err := c.awaitCurrentFrame(t.Context(), time.Millisecond)
	state := f.freshness(c)
	if err != nil || state.Status != "unconfirmed" || state.LagMS != 419900 {
		t.Fatalf("replay unexpectedly current: %+v %v", state, err)
	}
	c.onStatus([]byte(`{"up":601000}`))
	if c.boardVideoTS != 599900 {
		t.Fatalf("compact status moved the last captured AU: %d", c.boardVideoTS)
	}
	// No new capture for seven minutes is valid when the board reports that
	// same idle edge. Raw wall-clock age must not disqualify the frame.
	c.onStatus([]byte(`{"ap":"streaming","up":700000,"vage":520000}`))
	f, err = c.awaitCurrentFrame(t.Context(), time.Millisecond)
	if err != nil || f.freshness(c).Status != "current" {
		t.Fatalf("legitimate idle frame: %+v %v", f.freshness(c), err)
	}
}

func TestVerificationWaitsWithoutMovingOnBoardReplay(t *testing.T) {
	s, b := directBoard(t)
	c, err := s.conn(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.ffmpeg = "simulated"
	c.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, 100, 150))
	c.onStatus([]byte(`{"ap":"streaming","up":600000,"vage":0}`))
	c.onFrame(video.Frame{Img: img, TS: 180000})
	before := len(b.Commands)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = s.preparePointer(ctx, c, firstPosition); err != context.DeadlineExceeded || len(b.Commands) != before {
		t.Fatalf("verification moved or mistook the replay for an optical failure: %v %v", err, b.Commands[before:])
	}
}

func TestMirrorRequiresPostInputTimestampForLocator(t *testing.T) {
	s := &Server{}
	c := newConn("test", "test", "simulated")
	c.onStatus([]byte(`{"ap":"streaming","up":20000}`))
	offset := time.Now().Add(-time.Minute).UnixMilli()
	c.offset = &offset
	img := image.NewRGBA(image.Rect(0, 0, 100, 150))
	c.onFrame(video.Frame{Img: img, TS: 20})
	// A still, untouched screen remains valid indefinitely.
	before, err := s.currentFrame(t.Context(), c)
	if err != nil || before.freshness(c).Status != "current" {
		t.Fatalf("passive static frame: %v, %v", before, err)
	}
	c.noteInput("click", time.UnixMilli(offset+30))
	if before.freshness(c).Status != "unconfirmed" {
		t.Fatal("an input during OCR left an old result current")
	}
	if _, _, err := s.targetOnScreen(t.Context(), c, Request{Cmd: "tap-grid"}); err == nil || !strings.Contains(err.Error(), "after the last input") {
		t.Fatalf("attempted target lookup on a pre-input frame: %v", err)
	}
	// Repeated reads do not consume the pending input. A decoder receipt
	// timestamp cannot prove when the board captured an untagged replay.
	c.onFrame(video.Frame{Img: img})
	unknown, err := s.currentFrame(t.Context(), c)
	if err != nil || unknown.Image == nil || unknown.freshness(c).Status != "unconfirmed" {
		t.Fatalf("show the still image with unconfirmed freshness: %v", err)
	}
	c.onFrame(video.Frame{Img: img, TS: 40})
	confirmed, err := s.currentFrame(t.Context(), c)
	if err != nil || confirmed.freshness(c).Status != "current" || before.freshness(c).Status != "unconfirmed" {
		t.Fatalf("confirmation must apply only to the newer frame: %v", err)
	}
}

func TestLateBoardClockRedatesReplayedFrame(t *testing.T) {
	c := newConn("test", "test", "simulated")
	now := time.Now()
	c.status["ap"] = "streaming"
	c.learnClock(now, 20) // an old keyframe was the first thing delivered on reconnect
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	c.onFrame(video.Frame{Img: img, TS: 20})
	f := mirroredFrame{Image: img, boardTS: 20}
	c.noteInput("click", now.Add(-time.Second))
	if f.freshness(c).Status != "unconfirmed" {
		t.Fatal("replay TS alone cannot establish the live board clock")
	}
	c.onStatus([]byte(`{"up":10000}`))
	if f.freshness(c).Status != "unconfirmed" || c.captured.After(c.lastInput) {
		t.Fatal("cached replay kept its receipt time after the true board clock arrived")
	}
}
