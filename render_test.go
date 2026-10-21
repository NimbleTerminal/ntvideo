package ntvideo

import (
	"fmt"
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

func TestRasterWaitsForCapabilityAndRedrawsPausedFrame(t *testing.T) {
	for _, startup := range []bool{true, false} {
		t.Run(fmt.Sprintf("startup=%v", startup), func(t *testing.T) {
			// Reset any environment hints so this exercises unresolved discovery.
			session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{})
			session.SetKittyCapability(ntpicture.KittyCapabilityUnknown)
			defer session.Close()
			mode := RenderGlyph
			if startup {
				mode = RenderRaster
			}
			m := New(WithTerminalSession(session), WithRenderMode(mode), WithKittyMedium(ntpicture.KittyMediumDirect), WithWidth(4), WithHeight(2))
			defer m.Close()
			m.status.State = Paused
			drivePicture(t, m, m.pic.SetImage(solidFrame(color.White)))
			if !startup {
				drivePicture(t, m, m.SetRenderMode(RenderRaster))
			}
			if m.Status().Err != nil || m.pic.Status().Err != nil || m.Transport() != "glyph" {
				t.Fatal("pending detection became an error", m.Status(), m.pic.Status())
			}
			drivePicture(t, m, session.SetKittyCapability(ntpicture.KittyCapabilitySupported))
			if m.RenderMode() != RenderRaster || m.pic.Mode() != ntpicture.PictureKitty || !strings.Contains(m.View(), "\U0010EEEE") {
				t.Fatal("raster request did not redraw on detection")
			}
			if m.Status().State != Paused || m.Status().Err != nil || len(m.engine.pending) != 0 {
				t.Fatal("detection changed playback", m.Status())
			}
		})
	}
}
func TestRasterUnsupportedFallsBackAndCanRecover(t *testing.T) {
	session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilityUnsupported})
	defer session.Close()
	m := New(WithTerminalSession(session), WithRenderMode(RenderRaster), WithWidth(4), WithHeight(2), WithKittyMedium(ntpicture.KittyMediumDirect))
	defer m.Close()
	m.status.State = Paused
	drivePicture(t, m, m.pic.SetImage(solidFrame(color.White)))
	for i := 0; i < 3; i++ {
		drivePicture(t, m, m.Update(struct{}{}))
	}
	if m.Transport() != "glyph" || m.Status().Err != nil || m.pic.Status().Err != nil {
		t.Fatal("unsupported raster should retain glyph playback")
	}
	drivePicture(t, m, session.SetKittyCapability(ntpicture.KittyCapabilitySupported))
	if m.pic.Mode() != ntpicture.PictureKitty {
		t.Fatal("later support was ignored")
	}
	drivePicture(t, m, session.SetKittyCapability(ntpicture.KittyCapabilityUnsupported))
	if m.Transport() != "glyph" || m.Status().State != Paused {
		t.Fatal("capability loss did not fall back")
	}
}
func TestPendingRasterDoesNotOverrideNewMode(t *testing.T) {
	for _, mode := range []RenderMode{RenderGlyph, RenderAuto} {
		t.Run(mode.String(), func(t *testing.T) {
			session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{})
			session.SetKittyCapability(ntpicture.KittyCapabilityUnknown)
			defer session.Close()
			m := New(WithTerminalSession(session), WithRenderMode(RenderRaster))
			defer m.Close()
			drivePicture(t, m, m.SetRenderMode(mode))
			drivePicture(t, m, session.SetKittyCapability(ntpicture.KittyCapabilitySupported))
			if m.RenderMode() != mode || m.pic.AutoMode() != (mode == RenderAuto) {
				t.Fatal("late detection overrode user choice")
			}
			if mode == RenderGlyph && m.pic.Mode() != ntpicture.PictureGlyph {
				t.Fatal("glyph choice lost")
			}
		})
	}
}
func TestRasterUsesLatestSessionSnapshot(t *testing.T) {
	session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilityUnsupported})
	defer session.Close()
	m := New(WithTerminalSession(session), WithRenderMode(RenderRaster))
	defer m.Close()
	older := session.SetKittyCapability(ntpicture.KittyCapabilitySupported)
	newer := session.SetKittyCapability(ntpicture.KittyCapabilityUnsupported)
	drivePicture(t, m, newer)
	drivePicture(t, m, older)
	if m.pic.Mode() != ntpicture.PictureGlyph || m.Status().Err != nil {
		t.Fatal("old discovery notification selected raster")
	}
}

func TestRasterDiscoveryDuringFramePreparation(t *testing.T) {
	session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{})
	session.SetKittyCapability(ntpicture.KittyCapabilityUnknown)
	defer session.Close()
	m := New(WithTerminalSession(session), WithRenderMode(RenderRaster), WithKittyMedium(ntpicture.KittyMediumDirect), WithWidth(4), WithHeight(2))
	defer m.Close()
	m.engine.b = &fakeBackend{status: Status{State: Paused}, img: solidFrame(color.White)}
	pending := m.Update(m.sample()())
	drivePicture(t, m, session.SetKittyCapability(ntpicture.KittyCapabilitySupported))
	drivePicture(t, m, pending)
	if m.awaiting || m.pic.Mode() != ntpicture.PictureKitty || !strings.Contains(m.View(), "\U0010EEEE") {
		t.Fatal("discovery stalled or lost the pending frame")
	}
	if m.sample() == nil {
		t.Fatal("producer did not resume")
	}
}
