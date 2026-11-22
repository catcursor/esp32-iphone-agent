package daemon

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/proto"
	"github.com/onehumanbeing/esp32-iphone-agent/cli/internal/video"
)

// Synthetic mirrors used by the daemon tests bypass FFmpeg.
func (c *Conn) onFrame(f video.Frame) {
	c.mu.Lock()
	gen := c.decoderGen
	c.mu.Unlock()
	c.decodedFrame(gen, f)
}

func TestOldDecoderCannotReplaceNewSessionFrame(t *testing.T) {
	c := newConn("0123456789ab", "board", "")
	c.decoderGen = 2
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	c.decodedFrame(1, video.Frame{Img: img, TS: 31000})
	if c.Frame() != nil {
		t.Fatal("old decoder repopulated a new session's empty frame")
	}
	c.decodedFrame(2, video.Frame{Img: img, TS: 900})
	c.decodedFrame(1, video.Frame{Img: img, TS: 32000})
	if c.captureTS != 900 {
		t.Fatalf("new session's frame was replaced with an old timestamp: %d", c.captureTS)
	}
}

func TestWaitForMirrorKeyframe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newConn("0123456789ab", "board", "")
		c.onStatus([]byte(`{"ap":"streaming","fz":1}`))
		if err := c.waitForMirror(t.Context(), time.Hour); !errors.Is(err, errFrozen) {
			t.Fatalf("legacy firmware: %v", err)
		}
		c.onStatus([]byte(`{"ap":"streaming","fz":1,"kfr":1}`))
		done := make(chan error, 1)
		go func() { done <- c.waitForMirror(t.Context(), time.Minute) }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("returned before the new keyframe: %v", err)
		default:
		}
		c.onStatus([]byte(`{"fz":0}`))
		if err := <-done; err != nil {
			t.Fatalf("keyframe restored: %v", err)
		}
		c.onStatus([]byte(`{"fz":1}`))
		if err := c.waitForMirror(t.Context(), time.Second); err == nil || !strings.Contains(err.Error(), "waiting for a new mirroring keyframe") {
			t.Fatalf("timeout awaiting recovery: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := c.waitForMirror(ctx, time.Hour); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled recovery: %v", err)
		}
		c.links, c.sess = 1, &proto.Session{}
		go func() { done <- c.waitForMirror(t.Context(), time.Hour) }()
		synctest.Wait()
		c.mu.Lock()
		c.sess = nil
		c.signalFrameWaiters()
		c.mu.Unlock()
		if err := <-done; !errors.Is(err, proto.ErrSession) {
			t.Fatalf("disconnect while waiting: %v", err)
		}
	})
}

// noteApp takes s.mu while Server.conn / devices / closeAll hold s.mu and then
// take c.mu, so onStatus must release c.mu before invoking the app callback.
func TestOnStatusNotifiesAfterReleasingConnMu(t *testing.T) {
	c := newConn("0123456789ab", "test", "")

	called := false
	c.onApp = func() {
		called = true
		acquired := make(chan struct{})
		go func() {
			c.mu.Lock()
			c.mu.Unlock()
			close(acquired)
		}()
		select {
		case <-acquired:
		case <-time.After(3 * time.Second):
			// Return so onStatus can release c.mu; blocking here would hang the test.
			t.Error("app callback ran while c.mu was still held")
		}
	}

	c.onStatus([]byte(`{"owner":"app","busy":1}`))
	if !called {
		t.Fatal("app callback was not invoked for owner: app")
	}
}
