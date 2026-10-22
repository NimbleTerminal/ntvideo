//go:build (darwin || linux || windows) && (amd64 || arm64)

package ntvideo

import (
	"strings"
	"testing"
)

func TestMPVCallFailureIsReported(t *testing.T) {
	// A missing symbol is rejected by goffi before entering native code.
	invalid := &mpvFunction{}
	lib := &mpvLibrary{functions: map[string]*mpvFunction{
		"mpv_wait_event":            invalid,
		"mpv_set_property_string":   invalid,
		"mpv_render_context_create": invalid,
		"mpv_get_property":          invalid,
	}}
	b := &mpvBackend{lib: lib, done: make(chan struct{})}
	if err := b.Pause(); err == nil || !strings.Contains(err.Error(), "ABI call failed") {
		t.Fatalf("control did not report ABI failure: %v", err)
	}
	e := &engine{b: b}
	s, _ := e.sample(0, 0)
	if s.State != Failed || s.Err == nil || !strings.Contains(s.Err.Error(), "ABI call failed") {
		t.Fatalf("sample did not report ABI failure: %+v", s)
	}
	_ = b.number("duration")
	if b.controlErr == nil {
		t.Fatal("metadata read discarded ABI failure")
	}
	ready := make(chan error, 1)
	go b.renderLoop(ready)
	if err := <-ready; err == nil || !strings.Contains(err.Error(), "ABI call failed") {
		t.Fatalf("renderer did not report ABI failure: %v", err)
	}
	<-b.done
}
