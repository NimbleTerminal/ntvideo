package ntvideo

import (
	"io"
	"slices"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
)

// CaptionCue is one timestamped cue. ID is stable within a source and track,
// including when two cues have identical text. Text may contain multiple lines.
type CaptionCue struct {
	ID, TrackID string
	Start, End  time.Duration
	Text        string
}

type CueEventKind string

const (
	CueEnter CueEventKind = "enter"
	CueExit  CueEventKind = "exit"
)

type CueReason string

const (
	CuePlayback    CueReason = "playback"
	CueSeek        CueReason = "seek"
	CueTrackChange CueReason = "track-change"
	CueLoad        CueReason = "load"
	CueClose       CueReason = "close"
)

// CaptionCueMsg arrives on the Tea loop. SourceID changes on each Load; use
// (VideoID, SourceID, Cue.TrackID, Cue.ID) when deduplicating repeated playback.
// Delivery is sample-based, not real-time. Commands returned by handlers may
// run concurrently; hosts must serialize their own external side effects.
type CaptionCueMsg struct {
	VideoID  string
	SourceID uint64
	Cue      CaptionCue
	Kind     CueEventKind
	Reason   CueReason
}

// WithCaptionEvents enables timestamped events for native SRT/WebVTT sidecars
// and loaded browser TextTracks. It does not enable or select a caption track.
func WithCaptionEvents(enabled bool) Option { return func(c *config) { c.captionEvents = enabled } }

type cueBoundary struct {
	at    time.Duration
	index int
	kind  CueEventKind
}
type cueTimeline struct {
	cues       []CaptionCue
	boundaries []cueBoundary
}

func newCueTimeline(cues []CaptionCue) *cueTimeline {
	t := &cueTimeline{cues: cues}
	for i, c := range cues {
		t.boundaries = append(t.boundaries, cueBoundary{c.Start, i, CueEnter}, cueBoundary{c.End, i, CueExit})
	}
	sort.SliceStable(t.boundaries, func(i, j int) bool {
		a, b := t.boundaries[i], t.boundaries[j]
		if a.at != b.at {
			return a.at < b.at
		}
		if a.kind != b.kind {
			return a.kind == CueExit
		}
		return a.index < b.index
	})
	return t
}

type cueSnapshot struct {
	track      string
	timeline   *cueTimeline
	seekSerial uint64
}
type cueScheduler struct {
	timeline           *cueTimeline
	track              string
	active             map[int]bool
	position, duration time.Duration
	seekSerial         uint64
	started            bool
}

func (s *cueScheduler) clear(reason CueReason) []CaptionCueMsg {
	var events []CaptionCueMsg
	if s.timeline != nil {
		for i, c := range s.timeline.cues {
			if s.active[i] {
				c.TrackID = s.track
				events = append(events, CaptionCueMsg{Cue: c, Kind: CueExit, Reason: reason})
			}
		}
	}
	*s = cueScheduler{}
	return events
}
func (s *cueScheduler) observe(snapshot *cueSnapshot, now, duration time.Duration, reason CueReason, loop bool) []CaptionCueMsg {
	if snapshot == nil {
		return nil
	} // Loading or seeking: wait for settled metadata.
	var events []CaptionCueMsg
	emit := func(index int, kind CueEventKind, why CueReason) {
		if kind == CueEnter {
			if s.active[index] {
				return
			}
			s.active[index] = true
		} else {
			if !s.active[index] {
				return
			}
			delete(s.active, index)
		}
		c := s.timeline.cues[index]
		c.TrackID = s.track
		events = append(events, CaptionCueMsg{Cue: c, Kind: kind, Reason: why})
	}
	cross := func(from, to time.Duration) {
		if s.timeline == nil {
			return
		}
		start := sort.Search(len(s.timeline.boundaries), func(i int) bool { return s.timeline.boundaries[i].at > from })
		for _, b := range s.timeline.boundaries[start:] {
			if b.at > to {
				break
			}
			emit(b.index, b.kind, CuePlayback)
		}
	}
	reset := reason != "" || !s.started || s.track != snapshot.track || s.timeline != snapshot.timeline
	if reason == "" && reset {
		reason = CueTrackChange
		if !s.started {
			reason = CueLoad
		}
	}
	if !reset && now < s.position && loop && snapshot.seekSerial == s.seekSerial {
		// Finish the old traversal, then replay the start of the new one.
		cross(s.position, s.duration)
		events = append(events, s.clear(CuePlayback)...)
		s.timeline, s.track, s.active = snapshot.timeline, snapshot.track, map[int]bool{}
		cross(-1, now)
	} else {
		if !reset && (snapshot.seekSerial != s.seekSerial || now < s.position) {
			reset = true
			reason = CueSeek
		}
		if reset {
			events = append(events, s.clear(reason)...)
			s.timeline, s.track, s.active = snapshot.timeline, snapshot.track, map[int]bool{}
			if s.timeline != nil {
				for i, c := range s.timeline.cues {
					if c.Start <= now && now < c.End {
						emit(i, CueEnter, reason)
					}
				}
			}
		} else {
			cross(s.position, now)
		}
	}
	s.position, s.duration, s.seekSerial, s.started = now, duration, snapshot.seekSerial, true
	return events
}

func (m *Model) queueCueEvents(events []CaptionCueMsg) {
	for _, event := range events {
		event.VideoID = m.ID()
		event.SourceID = m.cueSource
		m.cuePending = append(m.cuePending, event)
	}
}
func (m *Model) flushCueEvents() tea.Cmd {
	if len(m.cuePending) == 0 {
		return nil
	}
	commands := make([]tea.Cmd, 0, len(m.cuePending))
	for _, event := range m.cuePending {
		commands = append(commands, func() tea.Msg { return event })
	}
	m.cuePending = nil
	return tea.Sequence(commands...)
}
func (m *Model) resetCues(reason CueReason) {
	if !m.cfg.captionEvents {
		return
	}
	m.queueCueEvents(m.cues.clear(reason))
	m.cueVersion++
	m.cueReason = reason
}

// CueTimeline is an immutable indexed subtitle cue list for headless and
// custom playback hosts. A nil *CueTimeline behaves as an empty timeline.
type CueTimeline struct {
	inner *cueTimeline
}

// NewCueTimeline indexes cues for random-access ActiveAt lookups and stateful
// CueCursor traversal. The input slice is copied, bounded to 10,000 valid cues
// and 4 MiB total text, and sanitized with the same terminal-safe plain-text
// rules as ParseCues.
func NewCueTimeline(cues []CaptionCue) *CueTimeline {
	sanitized := make([]CaptionCue, 0, min(len(cues), maxCaptionCues))
	total := 0
	for _, c := range cues {
		if len(sanitized) >= maxCaptionCues {
			break
		}
		if c.Start < 0 || c.End <= c.Start {
			continue
		}
		c.Text = captionPlainText(c.Text)
		if total+len(c.Text) > maxCueBytes {
			break
		}
		total += len(c.Text)
		sanitized = append(sanitized, c)
	}
	return &CueTimeline{inner: newCueTimeline(sanitized)}
}

// ParseCueTimeline parses bounded UTF-8 SRT or WebVTT cues from r (accepting
// CueSRT, CueWebVTT, or CueAuto) and indexes them into a CueTimeline.
func ParseCueTimeline(r io.Reader, format CueFormat) (*CueTimeline, error) {
	cues, err := ParseCues(r, format)
	if err != nil {
		return nil, err
	}
	return NewCueTimeline(cues), nil
}

// Len reports the number of cues in the timeline.
func (t *CueTimeline) Len() int {
	if t == nil || t.inner == nil {
		return 0
	}
	return len(t.inner.cues)
}

// Cues returns a copy of all cues in file order.
func (t *CueTimeline) Cues() []CaptionCue {
	if t == nil || t.inner == nil {
		return nil
	}
	return slices.Clone(t.inner.cues)
}

// ActiveAt returns cues active at pos (Start <= pos < End) in file order.
func (t *CueTimeline) ActiveAt(pos time.Duration) []CaptionCue {
	if t == nil || t.inner == nil || pos < 0 {
		return nil
	}
	var out []CaptionCue
	for _, c := range t.inner.cues {
		if c.Start <= pos && pos < c.End {
			out = append(out, c)
		}
	}
	return out
}

// CueCursor tracks active cues and boundary crossings across sampled playback
// positions using the same ordering, seek, track-change, and loop semantics as
// Model. CueCursor is not safe for concurrent use without synchronization.
type CueCursor struct {
	sched      cueScheduler
	timeline   *cueTimeline
	track      string
	seekSerial uint64
	reason     CueReason
}

// NewCueCursor creates a stateful cursor bound to timeline and trackID.
func NewCueCursor(timeline *CueTimeline, trackID string) *CueCursor {
	var inner *cueTimeline
	if timeline != nil {
		inner = timeline.inner
	}
	return &CueCursor{
		timeline: inner,
		track:    trackID,
	}
}

// SetTimeline switches the cursor's timeline or trackID. Any active cues from
// the previous timeline exit with CueTrackChange on the next Observe or Seek.
func (c *CueCursor) SetTimeline(timeline *CueTimeline, trackID string) {
	var inner *cueTimeline
	if timeline != nil {
		inner = timeline.inner
	}
	if c.sched.started && (c.timeline != inner || c.track != trackID) {
		c.reason = CueTrackChange
	}
	c.timeline = inner
	c.track = trackID
}

// Observe advances or reconciles the cursor at sampled media position pos and
// stream duration, emitting CueEnter and CueExit events in boundary order.
// When loop is true and pos wraps backward without an explicit Seek, remaining
// cues finish with CuePlayback before replaying from zero to pos.
func (c *CueCursor) Observe(pos, duration time.Duration, loop bool) []CaptionCueMsg {
	pos = max(0, pos)
	duration = max(0, duration)
	reason := c.reason
	c.reason = ""
	snap := &cueSnapshot{
		track:      c.track,
		timeline:   c.timeline,
		seekSerial: c.seekSerial,
	}
	return c.sched.observe(snap, pos, duration, reason, loop)
}

// Seek reconciles the cursor at an explicit seek destination, exiting prior
// active cues and entering destination cues with CueSeek without emitting
// intermediate cues.
func (c *CueCursor) Seek(pos, duration time.Duration) []CaptionCueMsg {
	pos = max(0, pos)
	duration = max(0, duration)
	c.seekSerial++
	c.reason = ""
	snap := &cueSnapshot{
		track:      c.track,
		timeline:   c.timeline,
		seekSerial: c.seekSerial,
	}
	return c.sched.observe(snap, pos, duration, CueSeek, false)
}

// Active returns the currently active cues in file order with TrackID populated.
func (c *CueCursor) Active() []CaptionCue {
	if c.sched.timeline == nil || len(c.sched.active) == 0 {
		return nil
	}
	var out []CaptionCue
	for i, cue := range c.sched.timeline.cues {
		if c.sched.active[i] {
			cue.TrackID = c.sched.track
			out = append(out, cue)
		}
	}
	return out
}

// Reset exits any currently active cues with reason (defaulting to CueClose)
// and clears cursor state.
func (c *CueCursor) Reset(reason CueReason) []CaptionCueMsg {
	if reason == "" {
		reason = CueClose
	}
	c.reason = ""
	return c.sched.clear(reason)
}
