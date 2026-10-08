package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntvideo"
)

type model struct {
	v                                          *ntvideo.Model
	width, height                              int
	expanded                                   bool
	source                                     string
	volumeDragging, volumePending, mutePending bool
	desiredVolume                              float64
	desiredMuted                               bool
}

func (m *model) Init() tea.Cmd { return m.v.Init() }
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var layout tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		layout = m.layout()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Sequence(m.v.Shutdown(), tea.Quit)
		case "space":
			layout = m.activate("play")
		case "enter":
			layout = m.activate("size")
		case "f":
			layout = m.activate("fit")
		case "left":
			layout = m.activate("back")
		case "right":
			layout = m.activate("forward")
		case "m":
			layout = m.activate("mute")
		case "+", "=":
			layout = m.activate("louder")
		case "-":
			layout = m.activate("quieter")
		}
	}
	mouse := m.mouse(msg)
	update := m.v.Update(msg)
	m.syncControls()
	return m, tea.Batch(layout, mouse, update)
}
func main() {
	source := "demo.mp4"
	if len(os.Args) > 1 {
		source = os.Args[1]
	}
	v := ntvideo.New(ntvideo.WithSource(source), ntvideo.WithScrubber(true), ntvideo.WithWidth(60), ntvideo.WithHeight(20), ntvideo.WithDescription("Video demonstration"))
	defer v.Close()
	v.Focus()
	m := &model{v: v, expanded: true, source: source}
	if err := run(m); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
