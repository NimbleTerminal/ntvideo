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
