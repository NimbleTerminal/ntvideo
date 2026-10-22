package ntvideo

import (
	"image"
	"time"
)

// FrameTransform processes a decoded native frame before either Kitty encoding
// or glyph conversion. It runs in the sampling command, not on the UI loop.
// The input and returned image must be treated as immutable. Return a new image
// to modify pixels, the input to bypass processing, or nil to keep the input.
// Position is the sampled media time. Browser DOM playback does not call it.
// Callbacks must not read mutable UI state and must be safe across models.
type FrameTransform func(image.Image, time.Duration) image.Image

// WithFrameTransform sets optional native frame processing. nil disables it.
func WithFrameTransform(f FrameTransform) Option {
	return func(c *config) { c.frameTransform = f }
}

// SetFrameTransform changes processing without restarting playback. Call from
// the UI loop. In-flight samples using the old transform are discarded; the
// next sample redraws even when paused. nil disables processing.
func (m *Model) SetFrameTransform(f FrameTransform) {
	if m.closed {
		return
	}
	m.cfg.frameTransform = f
	if m.dom {
		return
	}
	m.epoch++
	m.pictureCommands = append(m.pictureCommands, m.pic.Stop())
}
