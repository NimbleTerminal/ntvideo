package ntvideo

import (
	"errors"
	"image"
	"os"
	"testing"
	"time"
)

func fakePlayer(t *testing.T, o PlayerOptions) (*Player, *fakeBackend, *config, *int) {
	t.Helper()
	b := &fakeBackend{status: Status{State: Paused}}
	var got config
	calls := 0
	p := NewPlayer(o)
	p.create = func(c config) (backend, error) {
		calls++
		got = c
		return b, nil
	}
	return p, b, &got, &calls
}

func TestPlayerLoadsLazily(t *testing.T) {
	p, _, _, calls := fakePlayer(t, PlayerOptions{})
	if s, err := p.Status(); err != nil || s.State != Idle {
		t.Fatalf("status before load: %+v %v", s, err)
	}
	if img, err := p.Frame(10, 10); img != nil || err != nil {
		t.Fatalf("frame before load: %v %v", img, err)
	}
	if *calls != 0 {
		t.Fatal("backend created before Load")
	}
	if err := p.Load("a.mp4"); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("backend created %d times", *calls)
	}
}

func TestPlayerForwardsToBackend(t *testing.T) {
	p, b, _, _ := fakePlayer(t, PlayerOptions{})
	b.img = image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := p.Load("a.mp4"); err != nil {
		t.Fatal(err)
	}
	if err := p.Play(); err != nil {
		t.Fatal(err)
	}
	if err := p.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := p.Seek(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if img, err := p.Frame(4, 4); err != nil || img == nil {
		t.Fatalf("frame: %v %v", img, err)
	}
	want := []string{"load", "play", "pause", "seek"}
	if len(b.ops) != 4 || b.ops[0] != want[0] || b.ops[3] != want[3] || b.seeks[0] != 3*time.Second {
		t.Fatalf("ops %v seeks %v", b.ops, b.seeks)
	}
	if s, err := p.Status(); err != nil || s.State != Paused {
		t.Fatalf("status %+v %v", s, err)
	}
}

func TestPlayerOptionsReachConfig(t *testing.T) {
	wcfg := WorkerConfig{WorkerPath: "/trusted/worker", WorkerArgs: []string{"--worker"}, StagePath: "/trusted/stage"}
	p, _, c, _ := fakePlayer(t, PlayerOptions{
		DisableHardwareDecoding: true,
		DisableAudio:            true,
		Loop:                    true,
		CaptionEvents:           true,
		SuppressSubtitles:       true,
		LibraryPath:             "/trusted/libmpv",
		Worker:                  &wcfg,
	})
	wcfg.WorkerArgs[0] = "mutated"
	if err := p.Load("a.mp4"); err != nil {
		t.Fatal(err)
	}
	if !c.disableHW || !c.disableAudio || !c.muted || !c.loop || !c.captionEvents || !c.nativeCaptionOverlay || !c.useWorker || c.libraryPath != "/trusted/libmpv" || c.worker.WorkerPath != "/trusted/worker" || c.worker.WorkerArgs[0] != "--worker" {
		t.Fatalf("config %+v", c)
	}
}

func TestPlayerLoadSourceCueTimelineAndStep(t *testing.T) {
	p, b, _, _ := fakePlayer(t, PlayerOptions{CaptionEvents: true})
	tl := newCueTimeline([]CaptionCue{{ID: "0", Start: 0, End: time.Second, Text: "hi"}})
	b.status = Status{
		State:                Paused,
		CaptionCuesAvailable: true,
		cueData:              &cueSnapshot{track: "1", timeline: tl},
	}
	if err := p.LoadSource(Source{URL: "a.mp4", Captions: []Caption{{Data: []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), Default: true}}}); err != nil {
		t.Fatal(err)
	}
	s, err := p.Status()
	if err != nil || s.cueData != nil || !s.CaptionCuesAvailable {
		t.Fatalf("Status = %+v, %v", s, err)
	}
	gotTL, gotTrack := p.CueTimeline()
	if gotTL == nil || gotTL.Len() != 1 || gotTrack != "1" {
		t.Fatalf("CueTimeline = %v (%q)", gotTL, gotTrack)
	}
	if err := p.Step(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("fakeBackend without step should return ErrUnsupported, got %v", err)
	}
	if err := p.SelectTrack("captions", "1"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSuppressSubtitles(true); err != nil {
		t.Fatal(err)
	}
}

func TestPlayerLimitsReachConfig(t *testing.T) {
	want := Limits{MaxFileBytes: 7, MaxWidth: 640, MaxHeight: 480, MaxPixels: 640 * 480}
	p, _, c, _ := fakePlayer(t, PlayerOptions{Limits: want})
	if err := p.Load("a.mp4"); err != nil {
		t.Fatal(err)
	}
	if c.limits != want {
		t.Fatalf("limits %+v", c.limits)
	}
}

func TestPlayerFrameAdvancesBackend(t *testing.T) {
	p, b, _, _ := fakePlayer(t, PlayerOptions{})
	if err := p.Load("a.mp4"); err != nil {
		t.Fatal(err)
	}
	b.err = errors.New("decode failed")
	if img, err := p.Frame(4, 4); img != nil || !errors.Is(err, b.err) {
		t.Fatalf("frame without Status: %v %v", img, err)
	}
}

func TestPlayerCloseIdempotent(t *testing.T) {
	p, b, _, _ := fakePlayer(t, PlayerOptions{})
	if err := p.Load("a.mp4"); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if b.closed != 1 {
		t.Fatalf("closed %d times", b.closed)
	}
	if err := p.Load("b.mp4"); !errors.Is(err, ErrClosed) {
		t.Fatalf("load after close: %v", err)
	}
}

// Opt-in, as the other libmpv tests are.
func TestPlayerRealPlayback(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	p := NewPlayer(PlayerOptions{DisableHardwareDecoding: true, DisableAudio: true})
	defer p.Close()
	if err := p.Load(source); err != nil {
		t.Fatal(err)
	}
	if err := p.Play(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		img, err := p.Frame(160, 120)
		if err != nil {
			t.Fatal(err)
		}
		if img != nil {
			if _, ok := img.(*image.RGBA); !ok {
				t.Fatalf("expected *image.RGBA frame, got %T", img)
			}
			if err := p.Step(); err != nil {
				t.Fatalf("Step failed: %v", err)
			}
			s, err := p.Status()
			if err != nil {
				t.Fatal(err)
			}
			if s.Width <= 0 || s.Height <= 0 || s.FPS <= 0 {
				t.Fatalf("expected positive dimensions and FPS, got %+v", s)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no frame within 5s")
}
