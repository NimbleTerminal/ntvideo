package ntvideo

import (
	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture"
)

// RenderMode selects native terminal presentation. Browsers always use their
// HTML video surface. Raster requires a Kitty-graphics-compatible terminal.
type RenderMode int8

const (
	RenderAuto   RenderMode = iota // Detect Kitty graphics; otherwise use half-block glyphs.
	RenderGlyph                    // Always use ANSI half-block glyphs.
	RenderRaster                   // Request Kitty raster graphics explicitly.
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
	return m.pic.SetMode(mode.pictureMode())
}
