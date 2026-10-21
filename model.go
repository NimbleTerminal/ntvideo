package ntvideo

import (
	"errors"
	"fmt"
	"image"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/legacy"
)

var nextID atomic.Uint64

// Model follows legacy.Model's pointer-mutating component convention. Do not
// copy it after Init. Call its methods from the Bubble Tea event loop.
// Playback setters enqueue work in call order; Init/Update drives that work.
type Model struct {
	scrubbing                               bool
	scrubTarget                             time.Duration
	id                                      uint64
	cfg                                     config
	engine                                  *engine
	pic                                     legacy.Model
	status                                  Status
	started, closed, visible, focused, busy bool
	epoch                                   uint64
	dom                                     bool
	transport                               string
}
type sampleMsg struct {
	id, epoch uint64
	status    Status
	frame     image.Image
}
type wakeMsg struct{ id uint64 }
type encodedMsg struct {
	id, epoch uint64
	msg       tea.Msg
}
type presentedMsg struct{ id uint64 }

// EventMsg is emitted after a backend sample changes Status. Applications can
// inspect it or read Status after forwarding messages. IDs distinguish widgets.
type EventMsg struct {
	ID     string
	Status Status
}

func New(options ...Option) *Model {
	c := config{bounds: CellRect{Cols: 60, Rows: 20}, volume: 1, host: "terminal", description: "Video", kittyMedium: ntpicture.KittyMediumSharedMemory}
	for _, o := range options {
		o(&c)
	}
	c.bounds = normalize(c.bounds)
	id := nextID.Add(1)
	m := &Model{id: id, cfg: c, visible: true, dom: Capabilities().DOM, engine: &engine{cfg: c}, status: Status{State: Idle, Volume: c.volume, Muted: c.muted}}
	// Keep video IDs separate from picture's default/example IDs. Applications
	// mixing custom picture IDs should reserve this range for video widgets.
	m.pic = legacy.NewModelWithConfig(ntpicture.Config{KittyID: 0x560000 + int(id), Fit: c.fit.pictureFit(), KittyMedium: c.kittyMedium})
	r := mediaBounds(c)
	m.pic.SetSize(r.Cols, r.Rows)
	m.pic.SetImage(c.poster)
	m.Load(c.source)
	return m
}
func normalize(r CellRect) CellRect {
	r.Cols = max(0, min(1000, r.Cols))
	r.Rows = max(0, min(1000, r.Rows))
	return r
}
func (m *Model) ID() string { return fmt.Sprintf("ntvideo-%d", m.id) }
func (m *Model) Status() Status {
	s := m.status
	s.AudioTracks = append([]Track(nil), s.AudioTracks...)
	s.CaptionTracks = append([]Track(nil), s.CaptionTracks...)
	return s
}
func (m *Model) Bounds() CellRect { return m.cfg.bounds }
func (m *Model) Focus()           { m.focused = true }
func (m *Model) Blur()            { m.focused = false }
func (m *Model) Focused() bool    { return m.focused }
func (m *Model) Play()            { m.engine.enqueue(func(b backend) error { return b.Play() }) }
func (m *Model) Pause()           { m.engine.enqueue(func(b backend) error { return b.Pause() }) }
func (m *Model) Seek(t time.Duration) {
	t = max(0, t)
	m.epoch++
	m.engine.enqueue(func(b backend) error { return b.Seek(t) })
}
func (m *Model) SetVolume(v float64) {
	v = volume(v)
	m.engine.enqueue(func(b backend) error { return b.SetVolume(v) })
}
func (m *Model) SetMuted(v bool)            { m.engine.enqueue(func(b backend) error { return b.SetMuted(v) }) }
func (m *Model) SelectAudioTrack(id string) { m.selectTrack("audio", id) }

// An empty caption ID turns captions off.
func (m *Model) SelectCaptionTrack(id string) { m.selectTrack("captions", id) }
func (m *Model) selectTrack(kind, id string) {
	m.engine.enqueue(func(b backend) error { return b.SelectTrack(kind, id) })
}

// Load replaces the media. An empty URL records no media and asks the backend
// for nothing, so a poster-only widget never reports a load failure.
func (m *Model) Load(s Source) {
	if m.closed {
		return
	}
	s = cloneSource(s)
	m.cfg.source = s
	m.scrubbing = false
	m.epoch++
	if s.URL == "" {
		m.status.State = Idle
		return
	}
	m.status.State = Loading
	m.engine.enqueue(func(b backend) error { return b.Load(s) })
}
func (m *Model) SetBounds(r CellRect) tea.Cmd {
	if m.closed {
		return nil
	}
	r = normalize(r)
	m.cfg.bounds = r
	m.scrubbing = false
	r = mediaBounds(m.cfg)
	m.epoch++
	visible := m.visible
	m.engine.enqueue(func(b backend) error { return b.Layout(r, visible) })
	return m.pic.SetSize(r.Cols, r.Rows)
}
func (m *Model) SetSize(cols, rows int) tea.Cmd {
	r := m.cfg.bounds
	r.Cols = cols
	r.Rows = rows
	return m.SetBounds(r)
}
func (m *Model) SetVisible(v bool) tea.Cmd {
	if m.closed {
		return nil
	}
	m.visible = v
	m.scrubbing = false
	m.epoch++
	r := mediaBounds(m.cfg)
	m.engine.enqueue(func(b backend) error { return b.Layout(r, v) })
	if !v {
		return m.pic.SetImage(nil)
	}
	return nil
}
func (m *Model) Init() tea.Cmd {
	if m.started || m.closed {
		return nil
	}
	m.started = true
	if m.dom {
		return m.sample()
	}
	return tea.Batch(m.pic.Init(), m.sample())
}
func (m *Model) sample() tea.Cmd {
	if m.busy || m.closed {
		return nil
	}
	m.busy = true
	id, epoch, e := m.id, m.epoch, m.engine
	cw, ch := m.pic.CellPixelSize()
	r := mediaBounds(m.cfg)
	w, h := r.Cols*cw, r.Rows*ch
	// Bound CPU conversion and terminal bandwidth, preserving aspect ratio.
	scale := math.Min(1, math.Min(960/float64(max(1, w)), 540/float64(max(1, h))))
	// Avoid expanding the bounded media raster again before Kitty encoding.
	// The next SetImage supplies fresh pixels for this resolution.
	m.pic.SetKittyResolutionFactor(scale)
	w, h = max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	if !m.visible || r.Cols == 0 || r.Rows == 0 {
		w, h = 0, 0
	}
	return func() tea.Msg { s, img := e.sample(w, h); return sampleMsg{id, epoch, s, img} }
}
func (m *Model) next() tea.Cmd {
	m.busy = false
	if m.closed {
		return nil
	}
	id := m.id
	interval := time.Second / 30
	if m.dom {
		// The browser presents frames itself; samples only refresh Status.
		interval = time.Second / 10
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return wakeMsg{id} })
}
func statusEqual(a, b Status) bool {
	return a.State == b.State && a.CurrentTime == b.CurrentTime && a.Duration == b.Duration &&
		a.Volume == b.Volume && a.Muted == b.Muted && errorEqual(a.Err, b.Err) &&
		slices.Equal(a.AudioTracks, b.AudioTracks) && slices.Equal(a.CaptionTracks, b.CaptionTracks)
}
func errorEqual(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a == b || a.Error() == b.Error()
}
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if m.closed {
		return nil
	}
	m.updateScrubber(msg)
	switch msg := msg.(type) {
	case wakeMsg:
		if msg.id == m.id {
			return m.sample()
		}
		return nil
	case sampleMsg:
		if msg.id != m.id {
			return nil
		}
		var event tea.Cmd
		if !statusEqual(m.status, msg.status) {
			event = func() tea.Msg { return EventMsg{m.ID(), msg.status} }
		}
		m.status = msg.status
		if errors.Is(msg.status.Err, ErrUnsupported) {
			// This build has no backend. Report once and stop sampling.
			m.busy = false
			return event
		}
		if msg.epoch != m.epoch || msg.frame == nil || !m.visible {
			return tea.Batch(event, m.next())
		}
		cmd := m.pic.SetImage(msg.frame)
		if cmd == nil {
			return tea.Batch(event, m.next())
		}
		id, epoch := m.id, m.epoch
		return tea.Batch(event, func() tea.Msg { return encodedMsg{id, epoch, cmd()} })
	case encodedMsg:
		if msg.id != m.id {
			return nil
		}
		if msg.epoch != m.epoch {
			return m.next()
		}
		if frame, ok := msg.msg.(legacy.KittyFrameMsg); ok {
			m.transport = kittyTransport(frame)
		}
		id := m.id
		return tea.Sequence(m.pic.Update(msg.msg), func() tea.Msg { return presentedMsg{id} })
	case presentedMsg:
		if msg.id == m.id {
			return m.next()
		}
		return nil
	}
	if m.dom {
		return nil
	}
	cmd := m.pic.Update(msg)
	if m.pic.KittySupported() == ntpicture.KittyCapabilitySupported && m.pic.Mode() == ntpicture.PictureGlyph {
		return tea.Batch(cmd, m.pic.Toggle())
	}
	return cmd
}

// Transport names how frames reach the terminal: "dom" for the browser video
// element, "glyph" for half-blocks, "kitty" once Kitty graphics are selected, and
// "kitty/shm", "kitty/png", or "kitty/rgba" after a Kitty frame reports the medium
// it actually used. See WithKittyMedium.
func (m *Model) Transport() string {
	switch {
	case m.dom:
		return "dom"
	case m.pic.Mode() != ntpicture.PictureKitty:
		return "glyph"
	case m.transport != "":
		return m.transport
	}
	return "kitty"
}
func kittyTransport(frame legacy.KittyFrameMsg) string {
	switch {
	case frame.Medium == ntpicture.KittyMediumSharedMemory:
		return "kitty/shm"
	case frame.Format == ntpicture.KittyFormatRGBA:
		return "kitty/rgba"
	}
	return "kitty/png"
}

// View reserves exactly the configured cell rectangle. It has no DOM side effects.
func (m *Model) View() string {
	content := m.mediaView()
	if m.cfg.scrubber && m.cfg.bounds.Rows > 0 && m.cfg.bounds.Cols > 0 {
		if mediaBounds(m.cfg).Rows > 0 {
			content += "\n"
		}
		content += m.scrubberView()
	}
	return content
}
func (m *Model) mediaView() string {
	r := mediaBounds(m.cfg)
	if r.Cols == 0 || r.Rows == 0 {
		return ""
	}
	if m.visible && !m.dom {
		if s := m.pic.View().Content; s != "" {
			return s
		}
	}
	lines := make([]string, r.Rows)
	for i := range lines {
		lines[i] = strings.Repeat(" ", r.Cols)
	}
	if !m.dom && m.visible {
		label := "ntvideo: " + string(m.status.State)
		if m.status.Err != nil {
			label = "video unavailable"
		}
		if len(label) > r.Cols {
			label = label[:r.Cols]
		}
		lines[0] = label + strings.Repeat(" ", r.Cols-len(label))
	}
	return strings.Join(lines, "\n")
}

// Close always releases media resources, including after Program.Run returns.
// To delete Kitty resources while Tea is still running, use Shutdown before Quit.
func (m *Model) Close() error {
	if m.closed {
		return nil
	}
	m.closed = true
	m.epoch++
	m.status.State = Closed
	return m.engine.close()
}
func (m *Model) Shutdown() tea.Cmd { _ = m.Close(); return m.pic.SetImage(nil) }
