package ntvideo

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func TestFrameTransformBeforePresentation(t *testing.T) {
	for _, mode := range []RenderMode{RenderGlyph, RenderRaster} {
		m, b := fakeModel()
		defer m.Close()
		m.SetRenderMode(mode)
		original := solidFrame(color.White)
		transformed := solidFrame(color.Black)
		b.img = original
		b.status.CurrentTime = 2 * time.Second
		calls := 0
		m.SetFrameTransform(func(img image.Image, at time.Duration) image.Image {
			calls++
			if img != original || at != 2*time.Second {
				t.Fatal("transform did not receive decoded frame and position")
			}
			return transformed
		})
		msg := m.sample()().(sampleMsg)
		if calls != 1 || msg.frame != transformed {
			t.Fatal("transform was not applied before presentation")
		}
		drivePicture(t, m, m.Update(msg))
		if m.awaiting {
			t.Fatal("transform stalled presentation")
		}
	}
}
func TestFrameTransformToggleRejectsOldSamples(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	b.img = solidFrame(color.White)
	original := b.img
	m.SetFrameTransform(func(image.Image, time.Duration) image.Image { return solidFrame(color.Black) })
	pending := m.sample() // captures the old transform
	m.SetFrameTransform(nil)
	old := pending().(sampleMsg)
	if old.epoch == m.epoch {
		t.Fatal("old transform was not invalidated")
	}
	m.Update(old)
	if m.busy {
		t.Fatal("discarded frame stalled producer")
	}
	fresh := m.sample()().(sampleMsg)
	if fresh.frame != original {
		t.Fatal("disabled transform changed decoded frame")
	}
}

func TestFrameTransformToggleCancelsPreparedFrame(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	b.img = solidFrame(color.White)
	b.status.State = Paused
	drivePicture(t, m, m.Update(m.sample()()))
	before := m.View()
	m.SetFrameTransform(func(image.Image, time.Duration) image.Image { return solidFrame(color.Black) })
	pending := m.Update(m.sample()())
	if !m.awaiting {
		t.Fatal("expected pending picture preparation")
	}
	m.SetFrameTransform(nil)
	drivePicture(t, m, m.Update(struct{}{}))
	drivePicture(t, m, pending)
	if m.View() != before {
		t.Fatal("old transform replaced committed frame")
	}
	if m.awaiting {
		t.Fatal("transform change stalled presentation")
	}
	fresh := m.sample()
	if fresh == nil {
		t.Fatal("transform change stalled producer")
	}
	drivePicture(t, m, m.Update(fresh()))
	if m.awaiting || m.View() != before || m.Status().State != Paused {
		t.Fatal("disabled transform did not redraw the paused frame")
	}
}
