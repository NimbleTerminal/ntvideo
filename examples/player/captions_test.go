package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/ntvideo"
)

func TestCaptionMenuSelectionAndVisibility(t *testing.T) {
	m := &model{v: ntvideo.New(), width: 60, height: 20}
	defer m.v.Close()
	m.Update(tea.KeyPressMsg{Code: 'C', Text: "C"})
	if !m.captionMenu || m.v.Visible() {
		t.Fatal("menu did not hide video")
	}
	if !strings.Contains(m.View().Content, "No caption tracks") {
		t.Fatal("missing empty state")
	}
	m.captionChoices = []ntvideo.Track{{ID: "7", Label: "English", Language: "en"}, {ID: "9", Label: "Spanish"}}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.captionMenu || !m.v.Visible() || m.desiredCaption != "7" || m.lastCaption != "7" {
		t.Fatal("selection or visibility incorrect")
	}
	m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if m.desiredCaption != "" {
		t.Fatal("toggle did not disable captions")
	}
	m.openCaptions()
	m.captionKey("esc")
	if m.captionMenu || !m.v.Visible() {
		t.Fatal("cancel did not restore video")
	}
}

func TestCaptionMenuFitsAndSanitizesMetadata(t *testing.T) {
	m := &model{v: ntvideo.New(), captionMenu: true}
	defer m.v.Close()
	for range 30 {
		m.captionChoices = append(m.captionChoices, ntvideo.Track{ID: "1", Label: "\x1b[31mBad\nlabel\t"})
	}
	m.captionCursor = 30
	for _, size := range [][2]int{{60, 20}, {28, 12}, {10, 2}, {1, 1}} {
		m.width, m.height = size[0], size[1]
		view := m.View().Content
		if strings.ContainsAny(view, "\x1b\t") {
			t.Fatal("unsafe metadata")
		}
		lines := strings.Split(view, "\n")
		if len(lines) > m.height {
			t.Fatal("menu overflow")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > m.width {
				t.Fatal("wide menu")
			}
		}
	}
}

func TestCaptionTextToggleDoesNotChangeVideoMode(t *testing.T) {
	m := &model{v: ntvideo.New(ntvideo.WithRenderMode(ntvideo.RenderRaster))}
	defer m.v.Close()
	for _, want := range []bool{true, false} {
		m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
		if m.v.NativeCaptionOverlay() != want || m.v.RenderMode() != ntvideo.RenderRaster {
			t.Fatal("caption toggle changed video rendering")
		}
	}
}
