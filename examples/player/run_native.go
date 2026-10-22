//go:build !js

package main

import (
	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntvideo"
)

func reportCue(ntvideo.CaptionCueMsg) {} // Native demo reports the count in its header.

func run(m tea.Model) error { _, err := tea.NewProgram(m).Run(); return err }
