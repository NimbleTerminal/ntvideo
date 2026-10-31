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
	p, _, c, _ := fakePlayer(t, PlayerOptions{DisableHardwareDecoding: true, DisableAudio: true})
	if err := p.Load("a.mp4"); err != nil {
		t.Fatal(err)
	}
	if !c.disableHW || !c.disableAudio || !c.muted {
		t.Fatalf("config %+v", c)
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
		if _, err := p.Status(); err != nil {
			t.Fatal(err)
		}
		img, err := p.Frame(160, 120)
		if err != nil {
			t.Fatal(err)
		}
		if img != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no frame within 5s")
}
