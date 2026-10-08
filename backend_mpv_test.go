//go:build libmpv && cgo && !js

package ntvideo

import (
	"image"
	"math"
	"os"
	"testing"
	"time"
)

// Opt-in integration fixture avoids network and external media in unit tests.
func TestMPVPlayback(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE to a short local video")
	}
	raw, err := newBackend(config{volume: 0.5, muted: true})
	if err != nil {
		t.Fatal(err)
	}
	b := raw.(*mpvBackend)
	defer b.Close()
	if err = b.Load(Source{URL: source}); err != nil {
		t.Fatal(err)
	}
	if err = b.Play(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	var frame image.Image
	var status Status
	for time.Now().Before(deadline) {
		status, err = b.Poll()
		if err != nil {
			t.Fatal(err)
		}
		frame, err = b.Frame(160, 90)
		if err != nil {
			t.Fatal(err)
		}
		if frame != nil && status.CurrentTime > 100*time.Millisecond {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if frame == nil || status.CurrentTime <= 100*time.Millisecond || status.Duration <= 0 {
		t.Fatalf("no playback: %+v frame=%v", status, frame != nil)
	}
	if frame.Bounds().Dx() > 160 || frame.Bounds().Dy() > 90 {
		t.Fatal(frame.Bounds())
	}
	if b.aspect <= 0 || math.Abs(float64(frame.Bounds().Dx())/float64(frame.Bounds().Dy())-b.aspect) > 0.03 {
		t.Fatalf("source aspect ratio lost: frame=%v source=%v", frame.Bounds(), b.aspect)
	}
	first := frame.At(0, 0)
	different := false
	for y := 0; y < frame.Bounds().Dy(); y++ {
		for x := 0; x < frame.Bounds().Dx(); x++ {
			if frame.At(x, y) != first {
				different = true
			}
		}
	}
	if !different {
		t.Fatal("frame contains no image detail")
	}
	if err = b.Pause(); err != nil {
		t.Fatal(err)
	}
	if err = b.Seek(time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	status, err = b.Poll()
	if err != nil || status.State != Paused || status.CurrentTime < 900*time.Millisecond {
		t.Fatalf("pause/seek: %+v %v", status, err)
	}

	// A paused frame must be presented again after the surface becomes visible.
	if err = b.Layout(CellRect{}, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err = b.Layout(CellRect{}, true); err != nil {
		t.Fatal(err)
	}
	frame = nil
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frame, err = b.Frame(160, 90)
		if err != nil {
			t.Fatal(err)
		}
		if frame != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if frame == nil {
		t.Fatal("paused surface was not redrawn after becoming visible")
	}
	if err = b.SetVolume(0.3); err != nil {
		t.Fatal(err)
	}
	status, err = b.Poll()
	if err != nil || status.Volume < 0.29 || status.Volume > 0.31 || !status.Muted {
		t.Fatalf("audio control: %+v %v", status, err)
	}
}
