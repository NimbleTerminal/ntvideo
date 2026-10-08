package ntvideo

import (
	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// FitMode controls how video occupies its cell rectangle. Crops are centered.
type FitMode int8

const (
	FitContain FitMode = iota // Preserve aspect ratio, with letterboxing (default).
	FitCover                  // Preserve aspect ratio, cropping to fill the rectangle.
	FitFill                   // Stretch to fill the rectangle.
)

func normalizeFit(f FitMode) FitMode {
	if f < FitContain || f > FitFill {
		return FitContain
	}
	return f
}
func (f FitMode) String() string {
	switch normalizeFit(f) {
	case FitCover:
		return "cover"
	case FitFill:
		return "fill"
	default:
		return "contain"
	}
}
func (f FitMode) pictureFit() picture.FitMode {
	switch normalizeFit(f) {
	case FitCover:
		return picture.FitCover
	case FitFill:
		return picture.FitFill
	default:
		return picture.FitContain
	}
}
func WithFit(f FitMode) Option { return func(c *config) { c.fit = normalizeFit(f) } }
func (m *Model) Fit() FitMode  { return m.cfg.fit }

// SetFit changes presentation without restarting playback. Run its command to
// redraw an existing native frame, including while paused.
func (m *Model) SetFit(f FitMode) tea.Cmd {
	f = normalizeFit(f)
	if m.closed || f == m.cfg.fit {
		return nil
	}
	m.cfg.fit = f
	m.epoch++
	m.engine.enqueue(func(b backend) error { return b.SetFit(f) })
	return m.pic.SetFit(f.pictureFit())
}
