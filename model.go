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
)

var nextID atomic.Uint64

// Model follows ntpicture.Picture's pointer-mutating convention. Do not
// copy it after Init. Call its methods from the Bubble Tea event loop.
// Playback setters enqueue work in call order; Init/Update drives that work.
type Model struct {
	scrubbing                               bool
	scrubTarget                             time.Duration
	id                                      uint64
	cfg                                     config
	engine                                  *engine
	pic                                     *ntpicture.Picture
	status                                  Status
	started, closed, visible, focused, busy bool
	epoch                                   uint64
	dom                                     bool
	transport                               string
	awaiting                                bool
	waitGeneration                          uint64
	pictureCommands                         []tea.Cmd
	kept                                    bool
	cues                                    cueScheduler
	cueVersion, cueSource                   uint64
	cueReason                               CueReason
	cuePending                              []CaptionCueMsg
}
type sampleMsg struct {
	id, epoch  uint64
	status     Status
	frame      image.Image
	cueVersion uint64
}
type wakeMsg struct{ id uint64 }

// EventMsg is emitted after a backend sample changes Status. Applications can
// inspect it or read Status after forwarding messages. IDs distinguish widgets.
type EventMsg struct {
	ID     string
	Status Status
}

func New(options ...Option) *Model {
	c := config{nativeControls: true, bounds: CellRect{Cols: 60, Rows: 20}, volume: 1, host: "terminal", description: "Video", kittyMedium: ntpicture.KittyMediumSharedMemory}
	for _, o := range options {
		o(&c)
	}
	c.bounds = normalize(c.bounds)
	id := nextID.Add(1)
	m := &Model{id: id, cfg: c, visible: true, dom: Capabilities().DOM, engine: &engine{cfg: c}, status: Status{State: Idle, Volume: c.volume, Muted: c.muted}}
	m.pic = ntpicture.New(ntpicture.Options{Session: c.session, NextID: c.nextID, AutoMode: true, StableID: true, Render: ntpicture.Config{Fit: c.fit.pictureFit(), KittyMedium: c.kittyMedium, MaxRasterPixels: 960 * 540}})
	if !m.dom {
		m.pictureCommands = append(m.pictureCommands, m.applyRenderMode())
	}
	r := mediaBounds(c)
	m.pic.SetSize(r.Cols, r.Rows)
	// Store the poster until Init so no asynchronous command is discarded.
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
	s.cueData = nil
	return s
}
func (m *Model) Bounds() CellRect { return m.cfg.bounds }
func (m *Model) Focus()           { m.focused = true }
func (m *Model) Blur()            { m.focused = false }
func (m *Model) Focused() bool    { return m.focused }
func (m *Model) Play() {
	if m.visible {
		m.engine.enqueue(func(b backend) error { return b.Play() })
	}
}
func (m *Model) Pause() { m.engine.enqueue(func(b backend) error { return b.Pause() }) }
func (m *Model) Seek(t time.Duration) {
	if m.closed {
		return
	}
	t = max(0, t)
	m.resetCues(CueSeek)
	m.status.CaptionText = ""
	m.epoch++
	m.pictureCommands = append(m.pictureCommands, m.pic.Stop())
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
	if kind == "captions" {
		m.resetCues(CueTrackChange)
		m.status.CaptionText = ""
		m.epoch++
	}
	m.engine.enqueue(func(b backend) error { return b.SelectTrack(kind, id) })
}

// Load replaces the media. An empty URL records no media and asks the backend
// for nothing, so a poster-only widget never reports a load failure.
func (m *Model) Load(s Source) {
	if m.closed {
		return
	}
	s = cloneSource(s)
	m.resetCues(CueLoad)
	m.cueSource++
	m.cfg.source = s
	m.status.CaptionText = ""
	m.scrubbing = false
	m.epoch++
	if s.URL == "" {
		m.status.State = Idle
		return
	}
	m.status.State = Loading
	m.pictureCommands = append(m.pictureCommands, m.pic.Stop())
	visible := m.visible
	m.engine.enqueue(func(b backend) error {
		if err := b.Load(s); err != nil {
			return err
		}
		if !visible {
			return b.Pause()
		}
		return nil
	})
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
	if v == m.visible {
		return nil
	}
	m.visible = v
	m.scrubbing = false
	m.epoch++
	r := mediaBounds(m.cfg)
	m.engine.enqueue(func(b backend) error {
		if !v {
			if err := b.Pause(); err != nil {
				return err
			}
		}
		return b.Layout(r, v)
	})
	return m.pic.SetVisible(v)
}
func (m *Model) Init() tea.Cmd {
	if m.started || m.closed {
		return nil
	}
	m.started = true
	if m.dom {
		return tea.Batch(m.flushPictureCommands(), m.sample())
	}
	return tea.Batch(m.pic.Init(), m.pic.SetImage(m.cfg.poster), m.flushPictureCommands(), m.sample())
}
func (m *Model) sample() tea.Cmd {
	if m.busy || m.awaiting || m.closed {
		return nil
	}
	m.busy = true
	id, epoch, e := m.id, m.epoch, m.engine
	cw, ch := m.pic.CellPixelSize()
	r := mediaBounds(m.cfg)
	w, h := r.Cols*cw, r.Rows*ch
	// Bound CPU conversion and terminal bandwidth, preserving aspect ratio.
	scale := math.Min(1, math.Min(960/float64(max(1, w)), 540/float64(max(1, h))))
	// Picture independently bounds its prepared raster to the same pixel budget.
	w, h = max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	if !m.visible || r.Cols == 0 || r.Rows == 0 {
		w, h = 0, 0
	}
	cueVersion := m.cueVersion
	return func() tea.Msg {
		s, img := e.sample(w, h)
		return sampleMsg{id: id, epoch: epoch, status: s, frame: img, cueVersion: cueVersion}
	}
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
	return a.CaptionCuesAvailable == b.CaptionCuesAvailable && errorEqual(a.CaptionCueErr, b.CaptionCueErr) && a.CaptionText == b.CaptionText && a.State == b.State && a.CurrentTime == b.CurrentTime && a.Duration == b.Duration &&
		a.Volume == b.Volume && a.Muted == b.Muted && errorEqual(a.Err, b.Err) &&
		slices.Equal(a.AudioTracks, b.AudioTracks) && slices.Equal(a.CaptionTracks, b.CaptionTracks)
}
func errorEqual(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a == b || a.Error() == b.Error()
}
func (m *Model) Update(msg tea.Msg) (cmd tea.Cmd) {
	defer func() {
		if events := m.flushCueEvents(); events != nil {
			cmd = tea.Sequence(events, cmd)
		}
	}()
	// Even closed pictures must receive late preparation/retirement messages.
	pictureCmd := tea.Batch(m.flushPictureCommands(), m.pic.Update(msg))
	if m.closed {
		return pictureCmd
	}
	if !m.dom && m.cfg.renderMode == RenderRaster {
		pictureCmd = tea.Batch(pictureCmd, m.applyRenderMode())
	}
	m.updateScrubber(msg)
	switch msg := msg.(type) {
	case wakeMsg:
		if msg.id == m.id {
			return tea.Batch(pictureCmd, m.sample())
		}
		return pictureCmd
	case sampleMsg:
		if msg.id != m.id {
			return pictureCmd
		}
		if msg.epoch != m.epoch {
			// A sample from before a seek/load/track change must not restore an old cue.
			msg.status.CaptionText = ""
		}
		if m.cfg.captionEvents && m.cfg.source.URL != "" && msg.cueVersion == m.cueVersion && msg.status.cueData != nil {
			m.queueCueEvents(m.cues.observe(msg.status.cueData, msg.status.CurrentTime, msg.status.Duration, m.cueReason, m.cfg.loop))
			m.cueReason = ""
		}
		msg.status.cueData = nil
		var event tea.Cmd
		if !statusEqual(m.status, msg.status) {
			event = func() tea.Msg { return EventMsg{m.ID(), msg.status} }
		}
		m.status = msg.status
		if errors.Is(msg.status.Err, ErrUnsupported) {
			// This build has no backend. Report once and stop sampling.
			m.busy = false
			return tea.Batch(pictureCmd, event)
		}
		if msg.epoch != m.epoch || msg.frame == nil || !m.visible {
			return tea.Batch(pictureCmd, event, m.next())
		}
		m.busy = false
		cmd := m.pic.SetImageImmutable(msg.frame)
		m.awaiting = m.pic.Status().Busy
		m.waitGeneration = m.pic.Status().Generation
		if !m.awaiting {
			return tea.Batch(pictureCmd, event, cmd, m.next())
		}
		return tea.Batch(pictureCmd, event, cmd)
	case ntpicture.PresentedMsg:
		if msg.Owner == m.pic && msg.Generation == m.pic.Status().Generation {
			m.transport = kittyTransport(m.pic.Status())
			if m.awaiting {
				m.awaiting = false
				return tea.Batch(pictureCmd, m.next())
			}
		}
	case ntpicture.ErrorMsg:
		if msg.Owner == m.pic && msg.Generation == m.pic.Status().Generation {
			m.status.Err = msg.Err
			m.status.State = Failed
			status := m.Status()
			m.awaiting = false
			return tea.Batch(pictureCmd, func() tea.Msg { return EventMsg{m.ID(), status} }, m.next())
		}
	}
	// Hiding, seeking or resizing can cancel a pending generation without a
	// PresentedMsg. Resume the producer once Picture has drained its work.
	if m.awaiting && !m.pic.Status().Busy && m.pic.Status().Generation != m.waitGeneration {
		m.awaiting = false
		return tea.Batch(pictureCmd, m.next())
	}
	return pictureCmd
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
func kittyTransport(frame ntpicture.Status) string {
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
	if (m.visible || m.kept) && !m.dom {
		if s := m.pic.View(); s != "" {
			if m.cfg.nativeCaptionOverlay {
				return overlayCaptions(s, m.status.CaptionText, r.Cols, r.Rows)
			}
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
			if errors.Is(m.status.Err, ErrUnsupported) {
				label = "install mpv to play video"
			}
		}
		if len(label) > r.Cols {
			label = label[:r.Cols]
		}
		lines[0] = label + strings.Repeat(" ", r.Cols-len(label))
	}
	return strings.Join(lines, "\n")
}

// Release stops media resources without terminal output. It is safe after
// Program.Run returns. Use Close or Keep before quitting to finish presentation.
func (m *Model) Release() error {
	if m.closed {
		return nil
	}
	m.resetCues(CueClose)
	m.closed = true
	m.epoch++
	m.status.State = Closed
	return m.engine.close()
}

// Close releases media immediately and returns terminal cleanup. Execute the
// command before tea.Quit and continue forwarding late messages to Update.
func (m *Model) Close() tea.Cmd {
	_ = m.Release()
	return tea.Sequence(m.flushCueEvents(), tea.Batch(m.flushPictureCommands(), m.pic.Close()))
}

// Shutdown is an alias for Close.
func (m *Model) Shutdown() tea.Cmd { return m.Close() }

// Keep stops media and retains the last presented frame on the current screen.
func (m *Model) Keep() tea.Cmd {
	_ = m.Release()
	m.kept = true
	return tea.Sequence(m.flushCueEvents(), tea.Batch(m.flushPictureCommands(), m.pic.Keep()))
}

// KeepOnMainScreen retains the frame when the host leaves the alternate screen.
// Include View in the final main-screen view and sequence this command before Quit.
func (m *Model) KeepOnMainScreen() tea.Cmd {
	_ = m.Release()
	m.kept = true
	return tea.Sequence(m.flushCueEvents(), tea.Batch(m.flushPictureCommands(), m.pic.KeepOnMainScreen()))
}
func (m *Model) flushPictureCommands() tea.Cmd {
	cmds := m.pictureCommands
	m.pictureCommands = nil
	return tea.Batch(cmds...)
}

// Session is shared with the host when WithTerminalSession is supplied.
func (m *Model) Session() *ntpicture.TerminalSession { return m.pic.Session() }
func (m *Model) Position() time.Duration             { return m.status.CurrentTime }
func (m *Model) Duration() time.Duration             { return m.status.Duration }

func (m *Model) Closed() bool           { return m.closed }
func (m *Model) Visible() bool          { return m.visible }
func (m *Model) Size() (cols, rows int) { return m.cfg.bounds.Cols, m.cfg.bounds.Rows }
