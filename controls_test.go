package ntvideo

import (
	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
	"strings"
	"testing"
	"time"
)

func TestScrubberLayoutAndMouse(t *testing.T) {
	m := New(WithScrubber(true), WithWidth(50), WithHeight(10))
	b := &fakeBackend{}
	m.engine.b = b
	m.SetBounds(CellRect{X: 5, Y: 3, Cols: 50, Rows: 10})
	m.status.Duration = 100 * time.Second
	if r := mediaBounds(m.cfg); r.Rows != 9 || r.Y != 3 {
		t.Fatal(r)
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 10 || len(lines[9]) != 50 {
		t.Fatal("scrubber changed widget dimensions")
	}
	n, _ := m.scrubberParts()
	y := 12
	m.Update(tea.MouseClickMsg{X: 6, Y: y - 1, Button: tea.MouseLeft})
	m.engine.sample(0, 0)
	if len(b.seeks) != 0 {
		t.Fatal("video click sought playback")
	}
	m.Update(tea.MouseClickMsg{X: 6, Y: y, Button: tea.MouseLeft})
	m.engine.sample(0, 0)
	if len(b.seeks) != 1 || b.seeks[0] != 0 {
		t.Fatal(b.seeks)
	}
	m.Update(tea.MouseMotionMsg{X: 5 + n, Y: y + 1, Button: tea.MouseLeft})
	if m.scrubTarget != 100*time.Second || !strings.Contains(m.scrubberView(), "1:40 / 1:40") {
		t.Fatal(m.scrubberView())
	}
	m.engine.sample(0, 0)
	if len(b.seeks) != 1 {
		t.Fatal("drag flooded backend with seeks")
	}
	m.Update(tea.MouseReleaseMsg{X: 500, Y: y + 1, Button: tea.MouseLeft})
	m.engine.sample(0, 0)
	if len(b.seeks) != 2 || b.seeks[1] != 100*time.Second || m.scrubbing {
		t.Fatal(b.seeks)
	}
}
func TestScrubberUnavailableAndSmallSizes(t *testing.T) {
	for _, size := range []CellRect{{Cols: 0, Rows: 0}, {Cols: 1, Rows: 1}, {Cols: 2, Rows: 1}, {Cols: 3, Rows: 1}, {Cols: 20, Rows: 2}} {
		m := New(WithScrubber(true))
		b := &fakeBackend{}
		m.engine.b = b
		m.SetBounds(size)
		m.Update(tea.MouseClickMsg{X: 1, Y: size.Rows - 1, Button: tea.MouseLeft})
		m.engine.sample(0, 0)
		if len(b.seeks) != 0 {
			t.Fatal("unknown duration accepted seek")
		}
		m.status.Duration = time.Minute
		s := m.View()
		if size.Cols == 0 || size.Rows == 0 {
			if s != "" {
				t.Fatal(s)
			}
			continue
		}
		lines := strings.Split(s, "\n")
		if len(lines) != size.Rows {
			t.Fatal(size, len(lines))
		}
		for _, line := range lines {
			if len(line) != size.Cols {
				t.Fatal(size, line)
			}
		}
		m.SetVisible(false)
		m.Update(tea.MouseClickMsg{X: 1, Y: size.Rows - 1, Button: tea.MouseLeft})
		m.engine.sample(0, 0)
		if len(b.seeks) != 0 {
			t.Fatal("hidden scrubber accepted seek")
		}
	}
}
func TestFitChangesInvalidateFramesAndPreserveBounds(t *testing.T) {
	m, b := fakeModel()
	r := m.Bounds()
	epoch := m.epoch
	for _, fit := range []FitMode{FitCover, FitFill, FitContain} {
		m.SetFit(fit)
		m.engine.sample(0, 0)
		if b.fit != fit || m.Fit() != fit || m.pic.Fit() != fit.pictureFit() || m.Bounds() != r || m.epoch <= epoch {
			t.Fatal("fit update failed")
		}
		epoch = m.epoch
	}
	m.SetFit(FitCover)
	m.SetFit(FitMode(99))
	if m.Fit() != FitContain || m.pic.Fit() != picture.FitContain {
		t.Fatal("invalid mode did not normalize")
	}
	m.Close()
	m.SetFit(FitFill)
	if m.Fit() != FitContain {
		t.Fatal("closed model mutated")
	}
}
