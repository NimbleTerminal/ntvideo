package ntvideo

import (
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture"
)

func solidFrame(c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}
func TestPresentationPacesProducer(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	b.img = solidFrame(color.White)
	cmd := m.Update(m.sample()())
	if !m.awaiting || m.sample() != nil {
		t.Fatal("producer ran ahead of presentation")
	}
	drivePicture(t, m, cmd)
	if m.awaiting || m.pic.Status().Presented == 0 {
		t.Fatal("presentation did not release producer")
	}
	if m.sample() == nil {
		t.Fatal("producer stalled")
	}
}
func TestSeekCancelsPreparedFrame(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	b.img = solidFrame(color.White)
	drivePicture(t, m, m.Update(m.sample()()))
	before := m.View()
	b.img = solidFrame(color.Black)
	pending := m.Update(m.sample()())
	m.Seek(time.Second)
	drivePicture(t, m, m.Update(struct{}{}))
	drivePicture(t, m, pending)
	if m.View() != before {
		t.Fatal("stale pre-seek frame replaced committed frame")
	}
	if m.awaiting || m.sample() == nil {
		t.Fatal("seek stalled producer")
	}
}
func TestHidePausesAndShowDoesNotResume(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	m.Play()
	m.SetVisible(false)
	m.Play()
	m.SetVisible(true)
	m.engine.sample(0, 0)
	if got := strings.Join(b.ops, ","); got != "load,play,pause" {
		t.Fatal(got)
	}
}
func TestSharedSessionAllocatorAndKeep(t *testing.T) {
	session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported})
	defer session.Close()
	next := 700
	alloc := func() (int, error) { next++; return next, nil }
	newVideo := func() *Model {
		return New(WithTerminalSession(session), WithImageIDAllocator(alloc), WithWidth(2), WithHeight(2), WithKittyMedium(ntpicture.KittyMediumDirect))
	}
	a, b := newVideo(), newVideo()
	defer a.Close()
	defer b.Close()
	if a.Session() != session || b.Session() != session {
		t.Fatal("session not shared")
	}
	out := drivePicture(t, a, a.pic.SetImage(solidFrame(color.White)))
	out += drivePicture(t, b, b.pic.SetImage(solidFrame(color.White)))
	if !strings.Contains(out, "i=701") || !strings.Contains(out, "i=702") {
		t.Fatal("allocator bypassed")
	}
	view := a.View()
	drivePicture(t, a, a.Keep())
	drivePicture(t, a, a.Close())
	if a.View() != view || !a.closed {
		t.Fatal("Keep did not preserve view")
	}
	// Closing a component must not close the host's session or the other picture.
	out = drivePicture(t, b, b.SetSize(3, 3))
	if !strings.Contains(out, "i=703") || b.Session().Snapshot().Kitty != ntpicture.KittyCapabilitySupported {
		t.Fatal("shared picture damaged by close")
	}
}
func TestPictureErrorIsReported(t *testing.T) {
	failure := errors.New("ID budget exhausted")
	m := New(WithImageIDAllocator(func() (int, error) { return 0, failure }), WithWidth(2), WithHeight(2))
	defer m.Close()
	m.pic.SetTerminal(true, 8, 16)
	drivePicture(t, m, m.pic.SetImage(solidFrame(color.White)))
	if !errors.Is(m.Status().Err, failure) || m.Status().State != Failed {
		t.Fatal(m.Status())
	}
}
func TestCloseDuringPreparation(t *testing.T) {
	m := New(WithWidth(2), WithHeight(2))
	pending := m.pic.SetImage(solidFrame(color.White))
	drivePicture(t, m, m.Close())
	drivePicture(t, m, pending)
	if !m.pic.Closed() || m.pic.View() != "" {
		t.Fatal("late frame survived close")
	}
}
func TestUnrelatedPresentationDoesNotReleaseProducer(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	b.img = solidFrame(color.White)
	pending := m.Update(m.sample()())
	m.Update(ntpicture.PresentedMsg{Owner: ntpicture.New(ntpicture.Options{}), Generation: m.waitGeneration})
	m.Update(ntpicture.PresentedMsg{Owner: m.pic, Generation: m.waitGeneration - 1})
	if !m.awaiting {
		t.Fatal("unrelated presentation advanced video")
	}
	drivePicture(t, m, pending)
}
func TestResizeWhilePreparing(t *testing.T) {
	m, b := fakeModel()
	defer m.Close()
	b.img = solidFrame(color.White)
	pending := m.Update(m.sample()())
	resize := m.SetSize(4, 3)
	drivePicture(t, m, tea.Batch(pending, resize))
	if m.awaiting || len(strings.Split(m.View(), "\n")) != 3 {
		t.Fatal("resize did not settle")
	}
}
