package ntvideo

import (
	"errors"
	"image"
	"nimbleterminal.dev/ntpicture"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type fakeBackend struct {
	ops    []string
	seeks  []time.Duration
	fit    FitMode
	bounds CellRect
	closed int
	status Status
	img    image.Image
	err    error
}

func (b *fakeBackend) Load(Source) error { b.ops = append(b.ops, "load"); return nil }
func (b *fakeBackend) Play() error       { b.ops = append(b.ops, "play"); return nil }
func (b *fakeBackend) Pause() error      { b.ops = append(b.ops, "pause"); return nil }
func (b *fakeBackend) Seek(t time.Duration) error {
	b.ops = append(b.ops, "seek")
	b.seeks = append(b.seeks, t)
	return nil
}
func (b *fakeBackend) SetFit(f FitMode) error              { b.fit = f; return nil }
func (b *fakeBackend) SetVolume(float64) error             { return nil }
func (b *fakeBackend) SetMuted(bool) error                 { return nil }
func (b *fakeBackend) SelectTrack(string, string) error    { return nil }
func (b *fakeBackend) Layout(CellRect, bool) error         { return nil }
func (b *fakeBackend) Poll() (Status, error)               { return b.status, b.err }
func (b *fakeBackend) Frame(int, int) (image.Image, error) { return b.img, nil }
func (b *fakeBackend) Close() error                        { b.closed++; return nil }
func fakeModel() (*Model, *fakeBackend) {
	m := New(WithSource("clip.mp4"))
	b := &fakeBackend{status: Status{State: Paused}}
	m.engine.b = b
	return m, b
}

// messages runs a command and flattens any batch into the messages it yields.
func messages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, messages(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}
func TestCommandOrderAndClose(t *testing.T) {
	m, b := fakeModel()
	m.Play()
	m.Pause()
	m.Seek(time.Second)
	msg := m.sample()().(sampleMsg)
	m.Update(msg)
	if !reflect.DeepEqual(b.ops, []string{"load", "play", "pause", "seek"}) {
		t.Fatal(b.ops)
	}
	m.Close()
	m.Close()
	m.Play()
	if b.closed != 1 {
		t.Fatal(b.closed)
	}
	if cmd := m.Update(wakeMsg{m.id}); cmd != nil {
		t.Fatal("closed model scheduled work")
	}
	if s, _ := m.engine.sample(10, 10); !errors.Is(s.Err, ErrClosed) {
		t.Fatal(s)
	}
}
func TestIsolationAndStaleFrames(t *testing.T) {
	m, _ := fakeModel()
	other, _ := fakeModel()
	m.busy = true
	if m.Update(sampleMsg{id: other.id, status: Status{State: Playing}}) != nil || m.status.State == Playing {
		t.Fatal("cross-widget message")
	}
	epoch := m.epoch
	m.SetBounds(CellRect{Cols: 5, Rows: 3})
	m.Update(sampleMsg{id: m.id, epoch: epoch, frame: image.NewRGBA(image.Rect(0, 0, 1, 1))})
	if m.busy {
		t.Fatal("stale frame stalled scheduling")
	}
	if got := m.View(); len(strings.Split(got, "\n")) != 3 {
		t.Fatalf("bad rows %q", got)
	}
}
func TestBoundsAndVisibility(t *testing.T) {
	m, _ := fakeModel()
	m.SetBounds(CellRect{X: 3, Y: 4, Cols: 7, Rows: 2})
	m.SetVisible(false)
	if got := m.View(); got != "       \n       " {
		t.Fatalf("placeholder %q", got)
	}
	if m.Bounds().X != 3 {
		t.Fatal("lost origin")
	}
	m.SetSize(-1, 0)
	if m.View() != "" {
		t.Fatal("zero-sized view must be empty")
	}
}
func TestOneSampleInFlight(t *testing.T) {
	m, _ := fakeModel()
	cmd := m.sample()
	if m.sample() != nil {
		t.Fatal("concurrent sample")
	}
	msg := cmd()
	next := m.Update(msg)
	if next == nil || m.busy {
		t.Fatal("cycle did not finish")
	}
}
func TestSourceAndStatusOwnership(t *testing.T) {
	captions := []Caption{{Label: "original"}}
	m := New(WithMedia(Source{Captions: captions}))
	captions[0].Label = "changed"
	if m.cfg.source.Captions[0].Label != "original" {
		t.Fatal("source aliases caller")
	}
	m.status.AudioTracks = []Track{{ID: "1"}}
	s := m.Status()
	s.AudioTracks[0].ID = "2"
	if m.Status().AudioTracks[0].ID != "1" {
		t.Fatal("status aliases model")
	}
}
func TestInitIsIdempotent(t *testing.T) {
	m, _ := fakeModel()
	if m.Init() == nil {
		t.Fatal("missing init")
	}
	if m.Init() != nil {
		t.Fatal("second init")
	}
	m.Close()
}

func TestEmptySourceSkipsLoad(t *testing.T) {
	m := New(WithWidth(4), WithHeight(2))
	b := &fakeBackend{status: Status{State: Idle}}
	m.engine.b = b
	m.Update(m.sample()())
	if len(b.ops) != 0 || m.status.State != Idle || m.status.Err != nil {
		t.Fatalf("poster-only widget touched the backend: ops=%v status=%+v", b.ops, m.status)
	}
	m.Load(Source{URL: "clip.mp4"})
	m.Update(m.sample()())
	if !reflect.DeepEqual(b.ops, []string{"load"}) {
		t.Fatal(b.ops)
	}
}
func TestEventOnlyWhenStatusChanges(t *testing.T) {
	m, b := fakeModel()
	m.Update(m.sample()()) // first sample: load applied, state Paused
	count := func(cmd tea.Cmd) int {
		n := 0
		for _, msg := range messages(cmd) {
			if _, ok := msg.(EventMsg); ok {
				n++
			}
		}
		return n
	}
	if n := count(m.Update(m.sample()())); n != 0 {
		t.Fatalf("unchanged status emitted %d events", n)
	}
	b.status.CurrentTime = time.Second
	if n := count(m.Update(m.sample()())); n != 1 {
		t.Fatalf("changed status emitted %d events", n)
	}
	b.status.AudioTracks = []Track{{ID: "1"}}
	if n := count(m.Update(m.sample()())); n != 1 {
		t.Fatalf("track change emitted %d events", n)
	}
	b.err = errors.New("boom")
	if n := count(m.Update(m.sample()())); n != 1 {
		t.Fatalf("error emitted %d events", n)
	}
}

func TestErrorsPersistUntilNextOperation(t *testing.T) {
	m, b := fakeModel()
	b.err = errors.New("media failed")
	s, _ := m.engine.sample(1, 1)
	if s.Err == nil {
		t.Fatal("missing error")
	}
	b.err = nil
	s, _ = m.engine.sample(1, 1)
	if s.Err == nil || s.State != Failed {
		t.Fatal("error disappeared before application could handle it")
	}
	m.Play()
	s, _ = m.engine.sample(1, 1)
	if s.Err != nil {
		t.Fatal(s.Err)
	}
}

type blockingBackend struct {
	fakeBackend
	entered, release chan struct{}
}

func (b *blockingBackend) Poll() (Status, error) { close(b.entered); <-b.release; return Status{}, nil }
func TestEnqueueDoesNotWaitForBackend(t *testing.T) {
	m := New()
	b := &blockingBackend{entered: make(chan struct{}), release: make(chan struct{})}
	m.engine.b = b
	done := make(chan struct{})
	go func() { m.engine.sample(0, 0); close(done) }()
	<-b.entered
	queued := make(chan struct{})
	go func() { m.Play(); close(queued) }()
	select {
	case <-queued:
	case <-time.After(time.Second):
		close(b.release)
		t.Fatal("input blocked behind backend")
	}
	close(b.release)
	<-done
	m.Close()
}

func TestKittyMediumDefaultsToSharedMemory(t *testing.T) {
	m := New()
	if got := m.cfg.kittyMedium; got != ntpicture.KittyMediumSharedMemory {
		t.Fatalf("default medium = %v, want shared memory", got)
	}
	m = New(WithKittyMedium(ntpicture.KittyMediumDirect))
	if got := m.cfg.kittyMedium; got != ntpicture.KittyMediumDirect {
		t.Fatalf("medium = %v, want direct", got)
	}
}

func TestTransportReportsPresentedMedium(t *testing.T) {
	m := New()
	if m.dom {
		t.Skip("browser host presents through the DOM")
	}
	if got := m.Transport(); got != "glyph" {
		t.Fatalf("transport before Kitty = %q, want glyph", got)
	}
	m.transport = "kitty/shm"
	if got := m.Transport(); got != "glyph" {
		t.Fatalf("transport stays glyph until Kitty is selected, got %q", got)
	}
	for _, tc := range []struct {
		frame ntpicture.Status
		want  string
	}{
		{ntpicture.Status{Medium: ntpicture.KittyMediumSharedMemory, Format: ntpicture.KittyFormatRGBA}, "kitty/shm"},
		{ntpicture.Status{Medium: ntpicture.KittyMediumDirect, Format: ntpicture.KittyFormatRGBA}, "kitty/rgba"},
		{ntpicture.Status{Medium: ntpicture.KittyMediumDirect, Format: ntpicture.KittyFormatPNG}, "kitty/png"},
	} {
		if got := kittyTransport(tc.frame); got != tc.want {
			t.Fatalf("kittyTransport(%+v) = %q, want %q", tc.frame, got, tc.want)
		}
	}
}
