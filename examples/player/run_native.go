//go:build !js

package main

import tea "charm.land/bubbletea/v2"

func run(m tea.Model) error { _, err := tea.NewProgram(m).Run(); return err }
