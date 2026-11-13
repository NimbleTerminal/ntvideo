package ntvideo

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/ntpicture"
)

func TestCaptionOverlayPreservesImageCellsAndScrubber(t *testing.T) {
	for _, mode := range []RenderMode{RenderGlyph, RenderRaster} {
		t.Run(mode.String(), func(t *testing.T) {
			session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported})
			defer session.Close()
			m := New(WithTerminalSession(session), WithRenderMode(mode), WithNativeCaptionOverlay(true), WithScrubber(true), WithWidth(30), WithHeight(6))
			defer m.Close()
			drivePicture(t, m, m.pic.SetImage(solidFrame(color.White)))
			before := strings.Split(m.View(), "\n")
			m.status.CaptionText = "Hello 世界"
			after := strings.Split(m.View(), "\n")
			if len(after) != 6 || before[5] != after[5] || before[0] != after[0] {
				t.Fatal("overlay changed layout or scrubber")
			}
			for _, row := range after {
				if ansi.StringWidth(row) != 30 {
					t.Fatal("row width changed")
				}
			}
			if !strings.Contains(after[4], "Hello 世界") {
				t.Fatal("missing terminal text")
			}
			if mode == RenderRaster && !strings.Contains(after[4], "\U0010eeee") {
				t.Fatal("uncovered Kitty cells were lost")
			}
			m.status.CaptionText = ""
			if m.View() != strings.Join(before, "\n") {
				t.Fatal("expired captions left stale cells")
			}
		})
	}
}

func TestCaptionOverlayBoundsAndUntrustedText(t *testing.T) {
	for _, size := range [][2]int{{30, 8}, {3, 1}, {2, 2}, {0, 0}} {
		w, h := size[0], size[1]
		video := strings.TrimSuffix(strings.Repeat(strings.Repeat("x", w)+"\n", h), "\n")
		got := overlayCaptions(video, "\x1b[2J\x1b]2;injected\ahello\t世界\n"+strings.Repeat("a", 5000), w, h)
		if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "injected") || strings.ContainsAny(got, "\a\t") {
			t.Fatal("terminal injection")
		}
		if h == 0 {
			if got != video {
				t.Fatal("empty changed")
			}
			continue
		}
		lines := strings.Split(got, "\n")
		if len(lines) != h {
			t.Fatal("height changed")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != w {
				t.Fatalf("width: %q", line)
			}
		}
	}
}

func TestCaptionTextChangesAndClearing(t *testing.T) {
	if statusEqual(Status{CaptionText: "one"}, Status{CaptionText: "two"}) {
		t.Fatal("cue changes must emit status")
	}
	m := New()
	defer m.Close()
	for _, change := range []func(){func() { m.Seek(time.Second) }, func() { m.SelectCaptionTrack("") }, func() { m.Load(Source{}) }} {
		m.status.CaptionText = "old cue"
		oldEpoch := m.epoch
		change()
		if m.Status().CaptionText != "" {
			t.Fatal("stale cue")
		}
		m.Update(sampleMsg{id: m.id, epoch: oldEpoch, status: Status{CaptionText: "old cue"}})
		if m.Status().CaptionText != "" {
			t.Fatal("late sample restored stale cue")
		}
	}
}

func TestCaptionPlainTextRemovesBidiControls(t *testing.T) {
	for _, r := range []rune{'\u061c', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069'} {
		if got := captionPlainText("left" + string(r) + "right"); got != "leftright" {
			t.Fatalf("accepted U+%04X: %q", r, got)
		}
	}
	if got := captionPlainText("مرحبا 世界"); got != "مرحبا 世界" {
		t.Fatalf("altered ordinary Unicode text: %q", got)
	}
}

func TestOverlayCellsPreservesKittyAndAvoidsSGRGrowth(t *testing.T) {
	for _, mode := range []RenderMode{RenderGlyph, RenderRaster} {
		t.Run(mode.String(), func(t *testing.T) {
			session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported})
			defer session.Close()
			m := New(
				WithTerminalSession(session),
				WithRenderMode(mode),
				WithCaptionRenderMode(CaptionRenderHost),
				WithWidth(30),
				WithHeight(4),
			)
			defer m.Close()
			drivePicture(t, m, m.pic.SetImage(solidFrame(color.White)))
			m.status.CaptionText = "Host owns caption overlay"
			base := m.View()
			if strings.Contains(base, "Host owns caption overlay") {
				t.Fatal("CaptionRenderHost unexpectedly rendered built-in plain caption box")
			}

			cells := []OverlayCell{
				{X: 2, Y: 3, Symbol: "\x1b[2J♪ 世界\u202e!", Fg: color.RGBA{R: 255, G: 215, B: 0, A: 255}, Bg: color.Black, Bold: true},
				{X: -4, Y: 3, Symbol: "clip"},
				{X: 99, Y: 3, Symbol: "clip"},
			}
			first := OverlayCells(base, 30, 4, cells)
			if strings.Contains(first, "\x1b[2J") || strings.ContainsRune(first, '\u202e') {
				t.Fatal("OverlayCells leaked escape or bidi control")
			}
			rows := strings.Split(first, "\n")
			if len(rows) != 4 || rows[0] != strings.Split(base, "\n")[0] {
				t.Fatal("untouched rows changed")
			}
			for _, row := range rows {
				if ansi.StringWidth(row) != 30 {
					t.Fatalf("row width = %d, want 30", ansi.StringWidth(row))
				}
			}
			if !strings.Contains(rows[3], "♪ 世界!") {
				t.Fatalf("missing overlaid text in %q", rows[3])
			}
			if mode == RenderRaster && !strings.Contains(rows[3], "\U0010eeee") {
				t.Fatal("uncovered Kitty placeholder cells were lost")
			}

			repeated := first
			for range 25 {
				repeated = OverlayCells(repeated, 30, 4, cells)
			}
			if len(repeated) > len(first)+64 {
				t.Fatalf("repeated OverlayCells grew output from %d to %d bytes", len(first), len(repeated))
			}
		})
	}
}

func TestSpliceRowsAndModelReady(t *testing.T) {
	spliced := SpliceRows("TOP1\nTOP2\nTOP3", "bot1\nbot2\nbot3", 6, 3, 2)
	if spliced != "TOP1  \nTOP2  \nbot3  " {
		t.Fatalf("SpliceRows = %q", spliced)
	}

	m := New(WithSource("clip.mp4"), WithPoster(solidFrame(color.Black)), WithWidth(8), WithHeight(4))
	defer m.Close()
	b := &fakeBackend{status: Status{State: Paused}}
	m.engine.b = b
	drivePicture(t, m, m.Init())
	if m.Ready() {
		t.Fatal("poster frame must not mark Ready()")
	}
	b.img = solidFrame(color.White)
	drivePicture(t, m, m.Update(m.sample()()))
	if !m.Ready() {
		t.Fatal("expected Ready() after first media frame")
	}
	m.Load(Source{URL: "next.mp4"})
	if m.Ready() {
		t.Fatal("Load must reset Ready()")
	}
}
