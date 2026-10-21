package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/ntvideo"
)

const (
	ink    = "\x1b[38;2;214;223;235m"
	muted  = "\x1b[38;2;120;139;158m"
	accent = "\x1b[38;2;98;218;202m"
	border = "\x1b[38;2;66;90;109m"
)

func tint(c, s string) string { return c + s + "\x1b[0m" }
func pad(s string, w int) string {
	s = ansi.Truncate(s, max(0, w), "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

type hit struct {
	x, y, w int
	action  string
}

func (h hit) contains(x, y int) bool { return y == h.y && x >= h.x && x < h.x+h.w }

type panel struct {
	x, y, width, rows int
	compact           bool
}

func (m *model) panel() panel {
	if m.width < 28 || m.height < 12 {
		return panel{compact: true}
	}
	w, h := m.width-2, m.height-8
	if !m.expanded {
		w = min(60, w)
		h = min(20, h)
	}
	return panel{x: (m.width - w - 2) / 2, y: (m.height - h - 8) / 2, width: w, rows: h}
}
func (m *model) layout() tea.Cmd {
	m.volumeDragging = false
	p := m.panel()
	if p.compact {
		return m.v.SetBounds(ntvideo.CellRect{})
	}
	return m.v.SetBounds(ntvideo.CellRect{X: p.x + 1, Y: p.y + 3, Cols: p.width, Rows: p.rows})
}
func (m *model) volumeValue() float64 {
	if m.volumePending {
		return m.desiredVolume
	}
	return m.v.Status().Volume
}
func (m *model) mutedValue() bool {
	if m.mutePending {
		return m.desiredMuted
	}
	return m.v.Status().Muted
}
func (m *model) setVolume(v float64) {
	m.desiredVolume = max(0, min(1, v))
	m.volumePending = true
	m.v.SetVolume(m.desiredVolume)
}
func (m *model) toggleMute() {
	m.desiredMuted = !m.mutedValue()
	m.mutePending = true
	m.v.SetMuted(m.desiredMuted)
}
func (m *model) syncControls() {
	s := m.v.Status()
	if math.Abs(s.Volume-m.desiredVolume) < 0.001 {
		m.volumePending = false
	}
	if s.Muted == m.desiredMuted {
		m.mutePending = false
	}
	if s.Err != nil {
		m.volumePending = false
		m.mutePending = false
	}
}

// Controls and hit targets are built together, in cells, on every layout.
// No mutation from View and no hit areas for clipped buttons.
func (m *model) controlRows() (string, string, []hit) {
	p := m.panel()
	if p.compact {
		return "", "", nil
	}
	var hits []hit
	row := func(y int, buttons [][2]string) string {
		s := " "
		for _, b := range buttons {
			label := "[ " + b[0] + " ]"
			x := ansi.StringWidth(s)
			if x+len(label) > p.width {
				break
			}
			hits = append(hits, hit{p.x + 1 + x, y, len(label), b[1]})
			s += tint(accent, label) + " "
		}
		return pad(s, p.width)
	}
	play := "Play"
	if m.v.Status().State == ntvideo.Playing || m.v.Status().State == ntvideo.Buffering {
		play = "Pause"
	}
	size := "Expand"
	if m.expanded {
		size = "Small"
	}
	buttons := [][2]string{{play, "play"}, {"-5s", "back"}, {"+5s", "forward"}, {m.v.Fit().String(), "fit"}, {size, "size"}}
	controls := row(p.y+p.rows+4, buttons)
	mute := "Mute"
	if m.mutedValue() {
		mute = "Unmute"
	}
	vol := " " + tint(accent, "[ "+mute+" ]") + " " + tint(accent, "[-]") + " "
	muteWidth := len(mute) + 4
	hits = append(hits, hit{p.x + 2, p.y + p.rows + 5, muteWidth, "mute"}, hit{p.x + 3 + muteWidth, p.y + p.rows + 5, 3, "quieter"})
	start := ansi.StringWidth(vol)
	n := min(20, max(2, p.width-start-12))
	pos := int(math.Round(m.volumeValue() * float64(n-1)))
	slider := strings.Repeat("━", pos) + "●" + strings.Repeat("─", n-pos-1)
	vol += tint(accent, slider)
	hits = append(hits, hit{p.x + 1 + start, p.y + p.rows + 5, n, "volume"})
	vol += " " + tint(accent, "[+]")
	hits = append(hits, hit{p.x + 2 + start + n, p.y + p.rows + 5, 3, "louder"})
	vol += tint(ink, fmt.Sprintf(" %3d%%", int(math.Round(m.volumeValue()*100))))
	return controls, pad(vol, p.width), hits
}
func (m *model) activate(action string) tea.Cmd {
	switch action {
	case "play":
		if m.v.Status().State == ntvideo.Playing || m.v.Status().State == ntvideo.Buffering {
			m.v.Pause()
		} else {
			m.v.Play()
		}
	case "back":
		m.v.Seek(m.v.Status().CurrentTime - 5*time.Second)
	case "forward":
		m.v.Seek(m.v.Status().CurrentTime + 5*time.Second)
	case "render":
		if !ntvideo.Capabilities().DOM {
			return m.v.SetRenderMode((m.v.RenderMode() + 1) % 3)
		}
	case "fit":
		return m.v.SetFit((m.v.Fit() + 1) % 3)
	case "size":
		m.expanded = !m.expanded
		return m.layout()
	case "mute":
		m.toggleMute()
	case "quieter":
		m.setVolume(m.volumeValue() - 0.1)
	case "louder":
		m.setVolume(m.volumeValue() + 0.1)
	}
	return nil
}
func (m *model) volumeAt(x int) {
	_, _, hits := m.controlRows()
	for _, h := range hits {
		if h.action == "volume" {
			m.setVolume(float64(x-h.x) / float64(max(1, h.w-1)))
			return
		}
	}
}
func (m *model) mouse(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return nil
		}
		_, _, hits := m.controlRows()
		for _, h := range hits {
			if h.contains(msg.X, msg.Y) {
				if h.action == "volume" {
					m.volumeDragging = true
					m.volumeAt(msg.X)
					return nil
				}
				return m.activate(h.action)
			}
		}
	case tea.MouseMotionMsg:
		if m.volumeDragging {
			if msg.Button == tea.MouseLeft {
				m.volumeAt(msg.X)
			} else {
				m.volumeDragging = false
			}
		}
	case tea.MouseReleaseMsg:
		if m.volumeDragging && (msg.Button == tea.MouseLeft || msg.Button == tea.MouseNone) {
			m.volumeAt(msg.X)
			m.volumeDragging = false
		}
	}
	return nil
}
func (m *model) View() tea.View {
	p := m.panel()
	if p.compact {
		v := tea.NewView(pad("Resize to at least 28×12", m.width))
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	side := func(s string) string { return tint(border, "│") + pad(s, p.width) + tint(border, "│") }
	rule := func(left, right string) string { return tint(border, left+strings.Repeat("─", p.width)+right) }
	title := "  NTCHARTS " + tint(accent, "/ VIDEO") + "  "
	title = tint(border, "╭") + tint(ink, title) + tint(border, strings.Repeat("─", max(0, p.width-ansi.StringWidth(title)))+"╮")
	// Sources can come from a URL query in the browser; never echo escape sequences.
	name := ansi.Strip(filepath.Base(m.source))
	if name == "." || name == "" {
		name = "Video"
	}
	lines := []string{title, side(" " + tint(ink, name)), rule("├", "┤")}
	// Never recolor the picture content: Kitty embeds its image ID in foreground RGB.
	videoLines := strings.Split(m.v.View(), "\n")
	for i, line := range videoLines {
		if i == len(videoLines)-1 {
			line = tint(accent, strings.NewReplacer("=", "━", "-", "─", "O", "●").Replace(line))
		}
		lines = append(lines, side(line))
	}
	controls, volume, _ := m.controlRows()
	state := strings.ToUpper(string(m.v.Status().State))
	if transport := m.v.Transport(); transport != "" {
		state += " " + transport
	}
	hint := state + "  ·  f fit  ·  enter size  ·  q quit"
	if !ntvideo.Capabilities().DOM {
		hint = state + "  ·  r " + m.v.RenderMode().String() + "  ·  f fit  ·  enter size  ·  q quit"
	}
	if err := m.v.Status().Err; err != nil {
		hint = ansi.Strip(err.Error())
	}
	lines = append(lines, rule("├", "┤"), side(controls), side(volume), side(" "+tint(muted, hint)), rule("╰", "╯"))
	indent := strings.Repeat(" ", p.x)
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	v := tea.NewView(strings.Repeat("\n", p.y) + strings.Join(lines, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
