package ntvideo

import (
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
