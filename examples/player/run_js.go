//go:build js && wasm

package main

import (
	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/go-booba/wasm"
)

func run(m tea.Model) error { _, err := wasm.NewProgram(m).Run(); return err }
