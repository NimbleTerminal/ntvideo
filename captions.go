package ntvideo

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// CaptionRenderMode controls how native playback presents subtitles.
// Browsers always use HTML <track> presentation.
type CaptionRenderMode string

const (
	// CaptionRenderBackend lets libmpv rasterize subtitles into video frames (default).
	CaptionRenderBackend CaptionRenderMode = "backend"
	// CaptionRenderPlain suppresses libmpv subtitle rasterization and draws
	// ntvideo's built-in plain terminal caption box over View().
	CaptionRenderPlain CaptionRenderMode = "plain"
	// CaptionRenderHost suppresses libmpv subtitle rasterization and leaves
	// View() without a built-in caption box so the host can composite its own
	// terminal overlay (for example with OverlayCells).
	CaptionRenderHost CaptionRenderMode = "host"
)

func normalizeCaptionRenderMode(mode CaptionRenderMode) CaptionRenderMode {
	switch mode {
	case CaptionRenderPlain, CaptionRenderHost:
		return mode
	default:
		return CaptionRenderBackend
	}
}

// WithCaptionRenderMode selects native subtitle presentation. It has no effect
// in browsers. Bitmap subtitles have no text equivalent; keep CaptionRenderBackend
// for those tracks.
func WithCaptionRenderMode(mode CaptionRenderMode) Option {
	mode = normalizeCaptionRenderMode(mode)
	return func(c *config) {
		c.nativeCaptionOverlay = mode != CaptionRenderBackend
		c.hostCaptionOverlay = mode == CaptionRenderHost
	}
}

// WithNativeCaptionOverlay draws plain terminal captions over native video,
// instead of asking libmpv to rasterize them. It has no effect in browsers.
// Bitmap subtitles have no text equivalent; leave this disabled for those tracks.
func WithNativeCaptionOverlay(enabled bool) Option {
	if enabled {
		return WithCaptionRenderMode(CaptionRenderPlain)
	}
	return WithCaptionRenderMode(CaptionRenderBackend)
}

// CaptionRenderMode reports the configured native subtitle presentation mode.
func (m *Model) CaptionRenderMode() CaptionRenderMode {
	if m.cfg.nativeCaptionOverlay {
		if m.cfg.hostCaptionOverlay {
			return CaptionRenderHost
		}
		return CaptionRenderPlain
	}
	return CaptionRenderBackend
}

func (m *Model) NativeCaptionOverlay() bool {
	return !m.dom && m.CaptionRenderMode() == CaptionRenderPlain
}

// SetCaptionRenderMode switches native subtitle presentation without changing
// the selected track or playback position. Like Play, it queues work for Init/Update.
func (m *Model) SetCaptionRenderMode(mode CaptionRenderMode) {
	mode = normalizeCaptionRenderMode(mode)
	if m.closed || m.dom || m.CaptionRenderMode() == mode {
		return
	}
	prevSuppress := m.cfg.nativeCaptionOverlay
	m.cfg.nativeCaptionOverlay = mode != CaptionRenderBackend
	m.cfg.hostCaptionOverlay = mode == CaptionRenderHost
	m.epoch++
	m.pictureCommands = append(m.pictureCommands, m.pic.Stop())
	if prevSuppress != m.cfg.nativeCaptionOverlay {
		suppress := m.cfg.nativeCaptionOverlay
		m.engine.enqueue(func(b backend) error {
			if native, ok := b.(interface{ setCaptionOverlay(bool) error }); ok {
				return native.setCaptionOverlay(suppress)
			}
			return nil
		})
	}
}

// SetNativeCaptionOverlay switches presentation without changing the selected
// track or playback position. Like Play, it queues work for Init/Update.
func (m *Model) SetNativeCaptionOverlay(enabled bool) {
	if enabled {
		m.SetCaptionRenderMode(CaptionRenderPlain)
		return
	}
	m.SetCaptionRenderMode(CaptionRenderBackend)
}

// Only sanitized, bounded text may be mixed into a terminal image grid.
func captionPlainText(text string) string {
	text = strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\u061c' || r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			return -1
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(text))
	runes := []rune(text)
	if len(runes) > 4096 {
		text = string(runes[:4096])
	}
	return strings.TrimSpace(text)
}

// Replacing cells also removes their Kitty placeholders. Untouched cells keep
// their original image IDs and row/column diacritics; never recolor those cells.
func overlayCaptions(video, text string, width, height int) string {
	text = captionPlainText(text)
	if text == "" || width < 3 || height < 1 {
		return video
	}
	wrapped := strings.Split(ansi.Hardwrap(text, width-2, true), "\n")
	wrapped = wrapped[:min(len(wrapped), min(3, height))]
	lines := strings.Split(video, "\n")
	if len(lines) < height {
		return video
	}
	for i, line := range wrapped {
		line = " " + strings.TrimSpace(line) + " "
		w := ansi.StringWidth(line)
		x := (width - w) / 2
		y := height - len(wrapped) + i
		lines[y] = ansi.Cut(lines[y], 0, x) + "\x1b[0;97;40m" + line + "\x1b[0m" + ansi.Cut(lines[y], x+w, width)
	}
	return strings.Join(lines, "\n")
}
