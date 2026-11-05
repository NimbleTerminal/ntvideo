//go:build js && wasm

package main

import (
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"nimbleterminal.dev/booba/wasm"
	"nimbleterminal.dev/ntvideo"
	"syscall/js"
)

func run(m tea.Model) error { _, err := wasm.NewProgram(m).Run(); return err }

func reportCue(event ntvideo.CaptionCueMsg) {
	data, _ := json.Marshal(event)
	js.Global().Get("console").Call("debug", "ntvideo cue "+string(data))
}
