package ntvideo

import (
	"image/color"
	"strings"
	"testing"

	"nimbleterminal.dev/ntpicture"
)

func TestRenderModeSelectionAndPausedRedraw(t *testing.T) {
	session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported})
	defer session.Close()
	m := New(WithTerminalSession(session), WithRenderMode(RenderGlyph), WithKittyMedium(ntpicture.KittyMediumDirect), WithWidth(4), WithHeight(2))
	defer m.Close()
	if m.RenderMode() != RenderGlyph || m.pic.Mode() != ntpicture.PictureGlyph || m.pic.AutoMode() {
		t.Fatal("glyph option did not override detection")
	}
	m.status.State = Paused
	drivePicture(t, m, m.pic.SetImage(solidFrame(color.White)))
	if strings.Contains(m.View(), "\U0010EEEE") {
		t.Fatal("glyph view contains Kitty placeholders")
	}
	for _, mode := range []RenderMode{RenderRaster, RenderGlyph, RenderAuto} {
		drivePicture(t, m, m.SetRenderMode(mode))
		wantKitty := mode != RenderGlyph
		if strings.Contains(m.View(), "\U0010EEEE") != wantKitty {
			t.Fatalf("%s did not redraw paused frame", mode)
		}
		if m.Status().State != Paused || len(m.engine.pending) != 0 {
			t.Fatal("render switch changed playback")
		}
	}
	if !m.pic.AutoMode() {
		t.Fatal("auto detection not restored")
	}
}
func TestRenderSwitchDuringPreparation(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	drivePicture(t, m, m.pic.SetKittyCapability(ntpicture.KittyCapabilitySupported))
	drivePicture(t, m, m.SetRenderMode(RenderGlyph))
	b.img = solidFrame(color.White)
	pending := m.Update(m.sample()())
	drivePicture(t, m, m.SetRenderMode(RenderRaster))
	drivePicture(t, m, pending)
	if m.awaiting || m.pic.Mode() != ntpicture.PictureKitty {
		t.Fatalf("switch stalled producer: waiting=%v mode=%v status=%+v", m.awaiting, m.pic.Mode(), m.pic.Status())
	}
	if m.sample() == nil {
		t.Fatal("producer did not resume")
	}
}
func TestRenderModeDefaultsAndClosed(t *testing.T) {
	m := New()
	defer m.Close()
	if m.RenderMode() != RenderAuto || !m.pic.AutoMode() {
		t.Fatal("default should auto-detect")
	}
	m.SetRenderMode(RenderGlyph)
	m.SetRenderMode(RenderMode(99))
	if m.RenderMode() != RenderAuto {
		t.Fatal("invalid mode did not normalize")
	}
	m.Close()
	if m.SetRenderMode(RenderRaster) != nil || m.RenderMode() != RenderAuto {
		t.Fatal("closed model changed")
	}
}
