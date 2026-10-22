package ntvideo

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestCueParser(t *testing.T) {
	for _, text := range []string{
		"\ufeff1\r\n00:00:00,100 --> 00:00:00,200\r\n<b>Hello</b> &amp; world\r\nsecond line\r\n\r\n2\r\n00:00:00,200 --> 00:00:01,000\r\nHello\r\n",
		"WEBVTT\n\nNOTE ignored\ncomment\n\nfirst\n00:00.100 --> 00:00.200 align:start\n<b>Hello</b> &amp; world\nsecond line\n\nsecond\n00:00.200 --> 00:01.000\nHello\n",
	} {
		timeline, err := parseCaptionCues([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		if len(timeline.cues) != 2 || timeline.cues[0].Text != "Hello & world\nsecond line" || timeline.cues[0].Start != 100*time.Millisecond || timeline.cues[1].ID == timeline.cues[0].ID {
			t.Fatal(timeline.cues)
		}
		if timeline.boundaries[1].kind != CueExit || timeline.boundaries[2].kind != CueEnter {
			t.Fatal("equal boundary ordering")
		}
	}
	for _, text := range []string{"1\n00:00:02,000 --> 00:00:01,000\nx", "1\n00:99:00,000 --> 00:99:01,000\nx", "bad", "WEBVTT\nX-TIMESTAMP-MAP=LOCAL:00:00.000,MPEGTS:1\n\n", string([]byte{255}), strings.Repeat("a", maxCueBytes+1)} {
		if _, err := parseCaptionCues([]byte(text)); err == nil {
			t.Fatal("accepted invalid captions")
		}
	}
	tooMany := strings.Repeat("00:00:00,100 --> 00:00:00,200\nx\n\n", maxCaptionCues+1)
	if _, err := parseCaptionCues([]byte(tooMany)); err == nil {
		t.Fatal("accepted too many cues")
	}
}

func testTimeline() *cueTimeline {
	return newCueTimeline([]CaptionCue{
		{ID: "0", Start: time.Second, End: 1100 * time.Millisecond, Text: "same"},
		{ID: "1", Start: 1100 * time.Millisecond, End: 1200 * time.Millisecond, Text: "same"},
		{ID: "2", Start: 1050 * time.Millisecond, End: 2 * time.Second, Text: "overlap"},
	})
}
func eventNames(events []CaptionCueMsg) []string {
	names := make([]string, len(events))
	for i, e := range events {
		names[i] = string(e.Kind) + ":" + e.Cue.ID + ":" + string(e.Reason)
	}
	return names
}
func TestCueSchedulerCrossesEveryBoundary(t *testing.T) {
	s := cueScheduler{}
	snap := &cueSnapshot{track: "7", timeline: testTimeline()}
	s.observe(snap, 0, 3*time.Second, CueLoad, false)
	events := s.observe(snap, 1500*time.Millisecond, 3*time.Second, "", false)
	want := []string{"enter:0:playback", "enter:2:playback", "exit:0:playback", "enter:1:playback", "exit:1:playback"}
	if !reflect.DeepEqual(eventNames(events), want) {
		t.Fatal(eventNames(events))
	}
	for _, e := range events {
		if e.Cue.TrackID != "7" {
			t.Fatal(e)
		}
	}
	if got := s.observe(snap, 1500*time.Millisecond, 3*time.Second, "", false); len(got) != 0 {
		t.Fatal("pause repeated cue", got)
	}
	if got := eventNames(s.observe(snap, 2*time.Second, 3*time.Second, "", false)); !reflect.DeepEqual(got, []string{"exit:2:playback"}) {
		t.Fatal(got)
	}
}
func TestCueSchedulerDiscontinuities(t *testing.T) {
	snap := &cueSnapshot{track: "7", timeline: testTimeline()}
	for _, reason := range []CueReason{CueSeek, CueTrackChange, CueLoad} {
		s := cueScheduler{}
		s.observe(snap, 1050*time.Millisecond, 3*time.Second, CueLoad, false)
		events := s.observe(snap, 1150*time.Millisecond, 3*time.Second, reason, false)
		if len(events) != 4 || events[0].Kind != CueExit || events[2].Kind != CueEnter {
			t.Fatal(events)
		}
		for _, e := range events {
			if e.Reason != reason {
				t.Fatal(e)
			}
		}
	}
	s := cueScheduler{}
	s.observe(snap, 0, 3*time.Second, CueLoad, false)
	snap.seekSerial++
	if got := eventNames(s.observe(snap, 1150*time.Millisecond, 3*time.Second, "", false)); !reflect.DeepEqual(got, []string{"enter:1:seek", "enter:2:seek"}) {
		t.Fatal(got)
	}
	if got := s.observe(nil, 0, 0, "", false); len(got) != 0 {
		t.Fatal("unsettled metadata emitted events")
	}
	if got := s.clear(CueClose); len(got) != 2 || got[0].Reason != CueClose {
		t.Fatal(got)
	}
	if got := s.clear(CueClose); len(got) != 0 {
		t.Fatal("duplicate close")
	}
}
func TestCueSchedulerLoopReplay(t *testing.T) {
	s := cueScheduler{}
	snap := &cueSnapshot{track: "1", timeline: testTimeline()}
	s.observe(snap, 1500*time.Millisecond, 3*time.Second, CueLoad, true)
	events := s.observe(snap, 1150*time.Millisecond, 3*time.Second, "", true)
	want := []string{"exit:2:playback", "enter:0:playback", "enter:2:playback", "exit:0:playback", "enter:1:playback"}
	if !reflect.DeepEqual(eventNames(events), want) {
		t.Fatal(eventNames(events))
	}
	// Browser seek notifications remain seeks even when looping is enabled.
	snap.seekSerial++
	events = s.observe(snap, 1050*time.Millisecond, 3*time.Second, "", true)
	for _, event := range events {
		if event.Reason != CueSeek {
			t.Fatal("seek mistaken for replay", event)
		}
	}
}

func collectCueCommands(cmd tea.Cmd) []CaptionCueMsg {
	var events []CaptionCueMsg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if event, ok := msg.(CaptionCueMsg); ok {
			events = append(events, event)
			return
		}
		v := reflect.ValueOf(msg)
		if v.IsValid() && v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				if sub, ok := v.Index(i).Interface().(tea.Cmd); ok {
					run(sub)
				}
			}
		}
	}
	run(cmd)
	return events
}
func TestCueModelLifecycleAndStaleSamples(t *testing.T) {
	m := New(WithCaptionEvents(true), WithSource("first.mp4"))
	defer m.Close()
	status := Status{CurrentTime: 1050 * time.Millisecond, Duration: 3 * time.Second, cueData: &cueSnapshot{track: "7", timeline: testTimeline()}}
	sample := func(version uint64) tea.Cmd {
		return m.Update(sampleMsg{id: m.id, epoch: m.epoch, cueVersion: version, status: status})
	}
	events := collectCueCommands(sample(m.cueVersion))
	if len(events) != 2 || events[0].VideoID != m.ID() || events[0].SourceID != 1 || events[0].Reason != CueLoad {
		t.Fatal(events)
	}
	m.SetNativeCaptionOverlay(true)
	if got := collectCueCommands(sample(m.cueVersion)); len(got) != 0 {
		t.Fatal("rendering emitted cue", got)
	}
	old := m.cueVersion
	m.Seek(time.Second)
	events = collectCueCommands(sample(old))
	if len(events) != 2 || events[0].Kind != CueExit || events[0].Reason != CueSeek {
		t.Fatal("stale sample or missing exits", events)
	}
	events = collectCueCommands(sample(m.cueVersion))
	if len(events) != 2 || events[0].Reason != CueSeek {
		t.Fatal(events)
	}
	m.Load(Source{URL: "second.mp4"})
	events = collectCueCommands(sample(m.cueVersion))
	if len(events) != 4 || events[0].SourceID != 1 || events[2].SourceID != 2 || events[0].Kind != CueExit || events[2].Kind != CueEnter {
		t.Fatal(events)
	}
	events = collectCueCommands(m.Close())
	if len(events) != 2 || events[0].Reason != CueClose || events[0].SourceID != 2 {
		t.Fatal(events)
	}
	if got := collectCueCommands(m.Close()); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestCueEventsRequireOptInAndSource(t *testing.T) {
	for _, m := range []*Model{New(WithSource("first.mp4")), New(WithCaptionEvents(true))} {
		status := Status{CurrentTime: 1050 * time.Millisecond, cueData: &cueSnapshot{track: "7", timeline: testTimeline()}}
		events := collectCueCommands(m.Update(sampleMsg{id: m.id, epoch: m.epoch, cueVersion: m.cueVersion, status: status}))
		if len(events) != 0 {
			t.Fatal("unexpected cue events", events)
		}
		m.Close()
	}
}
