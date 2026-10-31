package ntvideo

import (
	"image"
	"sync"
	"time"
)

// PlayerOptions configure a Player. Both options narrow what the decoder
// touches, for hosts that run it in a sandbox.
type PlayerOptions struct {
	DisableHardwareDecoding bool // software decoding only
	DisableAudio            bool // no audio output or decoding
}

// Player is playback without a terminal or Bubble Tea, for hosts that present
// frames themselves. The media backend, and so libmpv, is not loaded until
// the first Load. Native sources are local filenames. Call Status regularly:
// it advances the backend's state, which Frame depends on.
type Player struct {
	opts   PlayerOptions
	create func(config) (backend, error)
	mu     sync.Mutex
	b      backend
	closed bool
}

// NewPlayer returns an idle Player.
func NewPlayer(o PlayerOptions) *Player { return &Player{opts: o, create: newBackend} }

func (p *Player) with(fn func(backend) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if p.b == nil {
		return nil
	}
	return fn(p.b)
}

// Load opens a local file, paused. It replaces any file already loaded.
func (p *Player) Load(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if p.b == nil {
		b, err := p.create(config{volume: 1, muted: p.opts.DisableAudio, disableHW: p.opts.DisableHardwareDecoding, disableAudio: p.opts.DisableAudio})
		if err != nil {
			return err
		}
		p.b = b
	}
	return p.b.Load(Source{URL: path})
}

func (p *Player) Play() error  { return p.with(backend.Play) }
func (p *Player) Pause() error { return p.with(backend.Pause) }
func (p *Player) Seek(t time.Duration) error {
	return p.with(func(b backend) error { return b.Seek(t) })
}

// Status reports playback state; State is Idle before the first Load.
func (p *Player) Status() (Status, error) {
	s := Status{State: Idle}
	err := p.with(func(b backend) (err error) {
		s, err = b.Poll()
		return err
	})
	return s, err
}

// Frame returns the newest frame scaled to fit width×height, or nil when
// there is no new one. The image is owned by the caller.
func (p *Player) Frame(width, height int) (image.Image, error) {
	var img image.Image
	err := p.with(func(b backend) (err error) {
		img, err = b.Frame(width, height)
		return err
	})
	return img, err
}

// Close releases the backend. It is safe to call more than once.
func (p *Player) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.b == nil {
		return nil
	}
	return p.b.Close()
}
