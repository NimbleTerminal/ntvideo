//go:build !js && (!libmpv || !cgo)

package ntvideo

import (
	"errors"
	"testing"
)

func TestUnsupportedBuildReportsOnceAndStops(t *testing.T) {
	m := New(WithSource("clip.mp4"))
	cmd := m.Update(m.sample()())
	if !errors.Is(m.status.Err, ErrUnsupported) || m.status.State != Failed {
		t.Fatalf("status %+v", m.status)
	}
	if m.busy {
		t.Fatal("sampling stayed busy")
	}
	var event, wake bool
	for _, msg := range messages(cmd) {
		switch msg.(type) {
		case EventMsg:
			event = true
		case wakeMsg:
			wake = true
		}
	}
	if !event || wake {
		t.Fatalf("event=%v wake=%v; want one event and no further ticks", event, wake)
	}
	m.Play()
	m.SetBounds(CellRect{Cols: 3, Rows: 2})
	if n := len(m.engine.pending); n != 0 {
		t.Fatalf("%d operations retained without a backend", n)
	}
	if m.Update(wakeMsg{m.id}) == nil {
		t.Fatal("an explicit wake must still sample")
	}
	if n := len(m.engine.pending); n != 0 {
		t.Fatalf("%d operations retained after sampling", n)
	}
}
