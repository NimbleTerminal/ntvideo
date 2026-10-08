package ntvideo

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// WithScrubber reserves the last row of the widget for playback progress.
// The parent must enable tea.MouseModeCellMotion and keep SetBounds in sync
// with layout for clicking and dragging. Unknown durations disable seeking.
func WithScrubber(enabled bool) Option { return func(c *config) { c.scrubber = enabled } }
func mediaBounds(c config) CellRect {
	r := c.bounds
	if c.scrubber {
		r.Rows = max(0, r.Rows-1)
	}
	return r
}
func timestamp(t time.Duration) string {
	s := int(max(0, t) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
func (m *Model) scrubberParts() (int, string) {
	current := m.status.CurrentTime
	if m.scrubbing {
		current = m.scrubTarget
	}
	duration := "--:--"
	if m.status.Duration > 0 {
		duration = timestamp(m.status.Duration)
	}
	if m.status.Duration > 0 {
		current = min(current, m.status.Duration)
	}
	label := fmt.Sprintf(" %*s / %s", len(duration), timestamp(current), duration)
	if m.cfg.bounds.Cols-len(label) < 8 {
		label = ""
	}
	return max(0, m.cfg.bounds.Cols-len(label)-2), label
}
func (m *Model) scrubberView() string {
	width := m.cfg.bounds.Cols
	if !m.visible || width < 3 {
		return strings.Repeat(" ", width)
	}
	n, label := m.scrubberParts()
	bar := []byte(strings.Repeat("-", n))
	if m.status.Duration > 0 {
		current := m.status.CurrentTime
		if m.scrubbing {
			current = m.scrubTarget
		}
		pos := int(float64(n-1) * float64(max(0, min(current, m.status.Duration))) / float64(m.status.Duration))
		for i := 0; i < pos; i++ {
			bar[i] = '='
		}
		bar[pos] = 'O'
	}
	return "[" + string(bar) + "]" + label
}
func (m *Model) scrubTime(x int) time.Duration {
	n, _ := m.scrubberParts()
	if n <= 1 {
		return 0
	}
	pos := max(0, min(n-1, x-m.cfg.bounds.X-1))
	return time.Duration(float64(m.status.Duration) * float64(pos) / float64(n-1))
}
func (m *Model) updateScrubber(msg tea.Msg) {
	if !m.cfg.scrubber || !m.visible || m.cfg.bounds.Rows == 0 || m.cfg.bounds.Cols < 3 || m.status.Duration <= 0 {
		m.scrubbing = false
		return
	}
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		n, _ := m.scrubberParts()
		if msg.Button == tea.MouseLeft && msg.Y == m.cfg.bounds.Y+m.cfg.bounds.Rows-1 && msg.X >= m.cfg.bounds.X && msg.X < m.cfg.bounds.X+n+2 {
			m.scrubTarget = m.scrubTime(msg.X)
			m.scrubbing = true
			m.Seek(m.scrubTarget)
		}
	case tea.MouseMotionMsg:
		if m.scrubbing {
			if msg.Button == tea.MouseLeft {
				m.scrubTarget = m.scrubTime(msg.X)
			} else {
				m.scrubbing = false
			}
		}
	case tea.MouseReleaseMsg:
		if m.scrubbing && (msg.Button == tea.MouseLeft || msg.Button == tea.MouseNone) {
			m.scrubTarget = m.scrubTime(msg.X)
			m.scrubbing = false
			m.Seek(m.scrubTarget)
		}
	}
}
