package ntvideo

import (
	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture"
)

// RenderMode selects native terminal presentation. Browsers always use their
// HTML video surface. Raster uses glyphs until Kitty graphics support is confirmed.
type RenderMode int8

const (
	RenderAuto   RenderMode = iota // Detect Kitty graphics; otherwise use half-block glyphs.
	RenderGlyph                    // Always use ANSI half-block glyphs.
	RenderRaster                   // Prefer Kitty raster graphics, waiting for detection if needed.
)

func normalizeRenderMode(mode RenderMode) RenderMode {
	if mode < RenderAuto || mode > RenderRaster {
		return RenderAuto
	}
	return mode
}
func (mode RenderMode) String() string {
	switch normalizeRenderMode(mode) {
	case RenderGlyph:
		return "glyph"
	case RenderRaster:
		return "raster"
	default:
		return "auto"
	}
}
func (mode RenderMode) pictureMode() ntpicture.PictureMode {
	switch normalizeRenderMode(mode) {
	case RenderGlyph:
		return ntpicture.PictureGlyph
	case RenderRaster:
		return ntpicture.PictureKitty
	default:
		return ntpicture.PictureAuto
	}
}
func WithRenderMode(mode RenderMode) Option {
	return func(c *config) { c.renderMode = normalizeRenderMode(mode) }
}

// RenderMode reports the requested policy. Transport reports the active output.
func (m *Model) RenderMode() RenderMode { return m.cfg.renderMode }

// SetRenderMode redraws the current native frame without restarting playback.
// Execute the returned command, including when paused. Invalid modes use auto.
func (m *Model) SetRenderMode(mode RenderMode) tea.Cmd {
	mode = normalizeRenderMode(mode)
	if m.closed || mode == m.cfg.renderMode {
		return nil
	}
	m.cfg.renderMode = mode
	if m.dom {
		return nil
	}
	m.epoch++
	m.transport = ""
	return m.applyRenderMode()
}

// applyRenderMode keeps a raster request pending in glyph mode while discovery
// is unresolved or unsupported. Update rechecks the shared session snapshot;
// an old notification must never override a newer capability or user choice.
func (m *Model) applyRenderMode() tea.Cmd {
	mode := m.cfg.renderMode.pictureMode()
	if m.cfg.renderMode == RenderRaster && m.pic.KittySupported() != ntpicture.KittyCapabilitySupported {
		mode = ntpicture.PictureGlyph
	}
	if mode == m.pic.Mode() && !m.pic.AutoMode() {
		return nil
	}
	return m.pic.SetMode(mode)
}
