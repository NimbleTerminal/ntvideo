package ntvideo

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// WithNativeCaptionOverlay draws plain terminal captions over native video,
// instead of asking libmpv to rasterize them. It has no effect in browsers.
// Bitmap subtitles have no text equivalent; leave this disabled for those tracks.
func WithNativeCaptionOverlay(enabled bool) Option {
	return func(c *config) { c.nativeCaptionOverlay = enabled }
}

func (m *Model) NativeCaptionOverlay() bool { return !m.dom && m.cfg.nativeCaptionOverlay }

// SetNativeCaptionOverlay switches presentation without changing the selected
// track or playback position. Like Play, it queues work for Init/Update.
func (m *Model) SetNativeCaptionOverlay(enabled bool) {
	if m.closed || m.dom || m.cfg.nativeCaptionOverlay == enabled {
		return
	}
	m.cfg.nativeCaptionOverlay = enabled
	m.epoch++
	m.pictureCommands = append(m.pictureCommands, m.pic.Stop())
	m.engine.enqueue(func(b backend) error {
		if native, ok := b.(interface{ setCaptionOverlay(bool) error }); ok {
			return native.setCaptionOverlay(enabled)
		}
		return nil
	})
}

// Only sanitized, bounded text may be mixed into a terminal image grid.
func captionPlainText(text string) string {
	text = strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
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
