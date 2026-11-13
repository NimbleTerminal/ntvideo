package ntvideo

import (
	"image"
	"slices"
	"sync"
	"time"
)

// PlayerOptions configure a Player. The Disable options narrow what the
// decoder touches, for hosts that run it in a sandbox. Set Worker to decode
// out-of-process through ntmediabox instead of loading libmpv in-process.
type PlayerOptions struct {
	DisableHardwareDecoding bool          // software decoding only
	DisableAudio            bool          // no audio output or decoding
	Loop                    bool          // loop playback at EOF
	CaptionEvents           bool          // parse SRT/WebVTT sidecar timelines
	SuppressSubtitles       bool          // suppress backend-burned subtitles in frames
	Limits                  Limits        // zero fields use the defaults
	LibraryPath             string        // trusted libmpv shared library; empty uses loader defaults
	Worker                  *WorkerConfig // optional confined out-of-process worker
}

// Player is playback without a terminal or Bubble Tea, for hosts that present
// frames themselves. The media backend, and so libmpv or the worker helper, is
// not started until the first Load or LoadSource. Native sources are local
// filenames.
type Player struct {
	opts        PlayerOptions
	create      func(config) (backend, error)
	mu          sync.Mutex
	b           backend
	cueTimeline *cueTimeline
	cueTrack    string
	closed      bool
}

// NewPlayer returns an idle Player.
func NewPlayer(o PlayerOptions) *Player {
	if o.Worker != nil {
		w := *o.Worker
		w.WorkerArgs = slices.Clone(w.WorkerArgs)
		o.Worker = &w
	}
	return &Player{opts: o, create: createBackend}
}

func (p *Player) playerConfig() config {
	cfg := config{
		libraryPath:          p.opts.LibraryPath,
		volume:               1,
		muted:                p.opts.DisableAudio,
		disableHW:            p.opts.DisableHardwareDecoding,
		disableAudio:         p.opts.DisableAudio,
		loop:                 p.opts.Loop,
		captionEvents:        p.opts.CaptionEvents,
		nativeCaptionOverlay: p.opts.SuppressSubtitles,
		limits:               p.opts.Limits,
	}
	if p.opts.Worker != nil {
		cfg.useWorker = true
		cfg.worker = WorkerConfig{
			WorkerPath:       p.opts.Worker.WorkerPath,
			WorkerArgs:       slices.Clone(p.opts.Worker.WorkerArgs),
			StagePath:        p.opts.Worker.StagePath,
			AllowUnsandboxed: p.opts.Worker.AllowUnsandboxed,
		}
	}
	return cfg
}

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

func (p *Player) captureCueSnapshotLocked(s *Status) {
	if s.cueData != nil {
		p.cueTimeline = s.cueData.timeline
		p.cueTrack = s.cueData.track
		s.cueData = nil
	}
}

// Load opens a local file, paused. It replaces any file already loaded.
func (p *Player) Load(path string) error {
	return p.LoadSource(Source{URL: path})
}

// LoadSource opens a local video file and optional external or inline caption
// sidecars, paused. It replaces any source already loaded.
func (p *Player) LoadSource(s Source) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	p.cueTimeline = nil
	p.cueTrack = ""
	if p.b == nil {
		b, err := p.create(p.playerConfig())
		if err != nil {
			return err
		}
		p.b = b
	}
	return p.b.Load(s)
}

func (p *Player) Play() error  { return p.with(backend.Play) }
func (p *Player) Pause() error { return p.with(backend.Pause) }
func (p *Player) Seek(t time.Duration) error {
	return p.with(func(b backend) error { return b.Seek(t) })
}

// Step pauses playback if needed and advances one decoded video frame on
// in-process native backends. Backends without single-frame stepping (such as
// DOM or worker streams) return ErrUnsupported.
func (p *Player) Step() error {
	return p.with(func(b backend) error {
		st, ok := b.(interface{ step() error })
		if !ok {
			return ErrUnsupported
		}
		return st.step()
	})
}

// SelectTrack selects an "audio" or "captions" track by ID (empty disables).
func (p *Player) SelectTrack(kind, id string) error {
	return p.with(func(b backend) error { return b.SelectTrack(kind, id) })
}

// SetSuppressSubtitles toggles whether backend-rendered subtitles are suppressed
// in decoded frames.
func (p *Player) SetSuppressSubtitles(enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	p.opts.SuppressSubtitles = enabled
	if p.b == nil {
		return nil
	}
	if ov, ok := p.b.(interface{ setCaptionOverlay(bool) error }); ok {
		return ov.setCaptionOverlay(enabled)
	}
	return nil
}

// Status reports playback state; State is Idle before the first Load.
func (p *Player) Status() (Status, error) {
	s := Status{State: Idle}
	err := p.with(func(b backend) (err error) {
		s, err = b.Poll()
		p.captureCueSnapshotLocked(&s)
		return err
	})
	return s, err
}

// CueTimeline returns the active caption track's parsed CueTimeline and track ID
// when CaptionEvents is enabled and the backend has reported a timeline during
// Status or Frame polling.
func (p *Player) CueTimeline() (*CueTimeline, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cueTimeline == nil {
		return nil, ""
	}
	return &CueTimeline{inner: p.cueTimeline}, p.cueTrack
}

// Frame returns the newest frame scaled to fit width×height, or nil when
// there is no new one. The image is opaque and owned by the caller. Frame
// advances the backend itself, so it needs no calls to Status; an error it
// returns is not reported again by Status.
func (p *Player) Frame(width, height int) (image.Image, error) {
	var img image.Image
	err := p.with(func(b backend) (err error) {
		var s Status
		if s, err = b.Poll(); err != nil {
			return err
		}
		p.captureCueSnapshotLocked(&s)
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
	p.cueTimeline = nil
	p.cueTrack = ""
	if p.b == nil {
		return nil
	}
	return p.b.Close()
}
