package main

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"math"
	"nimbleterminal.dev/ntvideo"
	"strings"
	"testing"
)

func TestExpandAndResize(t *testing.T) {
	m := &model{v: ntvideo.New(ntvideo.WithScrubber(true))}
	defer m.v.Close()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if b := m.v.Bounds(); b.Cols != 60 || b.Rows != 20 {
		t.Fatal(b)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if b := m.v.Bounds(); b.Cols != 98 || b.Rows != 32 || b.Y != 3 || b.X != 1 {
		t.Fatal(b)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 25})
	if b := m.v.Bounds(); b.Cols != 78 || b.Rows != 17 {
		t.Fatal(b)
	}
	m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if m.v.Fit() != ntvideo.FitCover || m.v.Bounds().Cols != 78 {
		t.Fatal("fit changed layout")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if b := m.v.Bounds(); b.Cols != 60 || b.Rows != 17 {
		t.Fatal(b)
	}
}

func TestControlHitAreasAndVolume(t *testing.T) {
	m := &model{v: ntvideo.New(ntvideo.WithScrubber(true)), expanded: true}
	defer m.v.Close()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	click := func(action string) {
		t.Helper()
		_, _, hits := m.controlRows()
		for _, h := range hits {
			if h.action == action {
				m.Update(tea.MouseClickMsg{X: h.x, Y: h.y, Button: tea.MouseLeft})
				return
			}
		}
		t.Fatal("missing control", action)
	}
	click("quieter")
	click("quieter")
	if math.Abs(m.volumeValue()-0.8) > 0.001 {
		t.Fatal(m.volumeValue())
	}
	click("louder")
	if math.Abs(m.volumeValue()-0.9) > 0.001 {
		t.Fatal(m.volumeValue())
	}
	click("mute")
	if !m.mutedValue() {
		t.Fatal("mute click ignored")
	}
	click("mute")
	if m.mutedValue() {
		t.Fatal("unmute click ignored")
	}
	_, _, hits := m.controlRows()
	for _, h := range hits {
		if h.action == "volume" {
			m.Update(tea.MouseClickMsg{X: h.x, Y: h.y, Button: tea.MouseLeft})
			if m.volumeValue() != 0 {
				t.Fatal("slider start")
			}
			m.Update(tea.MouseMotionMsg{X: h.x + h.w + 10, Y: h.y, Button: tea.MouseLeft})
			if m.volumeValue() != 1 {
				t.Fatal("slider clamping")
			}
			m.Update(tea.MouseReleaseMsg{X: h.x + h.w - 1, Y: h.y, Button: tea.MouseLeft})
			if m.volumeDragging {
				t.Fatal("drag remained active")
			}
		}
	}
	click("fit")
	if m.v.Fit() != ntvideo.FitCover {
		t.Fatal("fit click ignored")
	}
	click("size")
	if m.expanded || m.v.Bounds().X <= 1 {
		t.Fatal("small player not centered")
	}
}
func TestPanelFitsWindowAndControls(t *testing.T) {
	for _, size := range [][2]int{{100, 40}, {62, 28}, {40, 16}, {28, 12}, {20, 8}} {
		m := &model{v: ntvideo.New(ntvideo.WithScrubber(true)), expanded: true}
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) > size[1] {
			t.Fatal(size, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal(size, ansi.StringWidth(line))
			}
		}
		_, _, hits := m.controlRows()
		for _, h := range hits {
			if h.y >= len(lines) || h.x+h.w > size[0] {
				t.Fatal("offscreen hit area", size, h)
			}
		}
		m.v.Close()
	}
}

func TestRenderKeyCyclesModesWithoutChangingLayout(t *testing.T) {
	m := &model{v: ntvideo.New()}
	defer m.v.Close()
	bounds := m.v.Bounds()
	for _, want := range []ntvideo.RenderMode{ntvideo.RenderGlyph, ntvideo.RenderRaster, ntvideo.RenderAuto} {
		m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
		if m.v.RenderMode() != want || m.v.Bounds() != bounds {
			t.Fatal("render key changed layout or selected wrong mode")
		}
	}
}
