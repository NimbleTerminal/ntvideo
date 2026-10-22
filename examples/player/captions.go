package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *model) selectedCaption() string {
	if m.captionPending {
		return m.desiredCaption
	}
	for _, track := range m.v.Status().CaptionTracks {
		if track.Selected {
			return track.ID
		}
	}
	return ""
}

func (m *model) selectCaption(id string) {
	if id != "" {
		m.lastCaption = id
	}
	m.desiredCaption, m.captionPending = id, true
	m.v.SelectCaptionTrack(id)
}

func (m *model) toggleCaptions() {
	if id := m.selectedCaption(); id != "" {
		m.lastCaption = id
		m.selectCaption("")
		return
	}
	tracks := m.v.Status().CaptionTracks
	for _, track := range tracks {
		if track.ID == m.lastCaption {
			m.selectCaption(track.ID)
			return
		}
	}
	if len(tracks) > 0 {
		m.selectCaption(tracks[0].ID)
	}
}

func (m *model) openCaptions() tea.Cmd {
	m.captionChoices = m.v.Status().CaptionTracks
	m.captionCursor = 0 // Off is always the first choice.
	for i, track := range m.captionChoices {
		if track.ID == m.selectedCaption() {
			m.captionCursor = i + 1
		}
	}
	m.captionMenu = true
	m.volumeDragging = false
	return m.v.SetVisible(false)
}

func (m *model) captionKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		m.captionCursor = max(0, m.captionCursor-1)
	case "down", "j":
		m.captionCursor = min(len(m.captionChoices), m.captionCursor+1)
	case "enter":
		id := ""
		if m.captionCursor > 0 {
			id = m.captionChoices[m.captionCursor-1].ID
		}
		m.selectCaption(id)
		fallthrough
	case "esc", "C":
		m.captionMenu = false
		return m.v.SetVisible(true)
	}
	return nil
}

func (m *model) captionView() tea.View {
	labels := []string{"Off"}
	for _, track := range m.captionChoices {
		label := track.Label
		if label == "" {
			label = "Track " + track.ID
		}
		if track.Language != "" {
			label += " (" + track.Language + ")"
		}
		// Track metadata is untrusted, just like media filenames.
		label = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, ansi.Strip(label))
		labels = append(labels, label)
	}
	lines := []string{"Captions · ↑/↓ choose · Enter select · Esc cancel"}
	if len(m.captionChoices) == 0 {
		lines = append(lines, "No caption tracks available")
	}
	rows := max(1, m.height-len(lines))
	start := max(0, m.captionCursor-rows+1)
	for i := start; i < min(len(labels), start+rows); i++ {
		cursor := "  "
		if i == m.captionCursor {
			cursor = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%s", cursor, labels[i]))
	}
	for i := range lines {
		lines[i] = pad(lines[i], m.width)
	}
	view := tea.NewView(strings.Join(lines[:min(len(lines), max(0, m.height))], "\n"))
	view.AltScreen = true
	return view
}
