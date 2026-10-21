// Package ntvideo provides a Bubble Tea v2 video component. Media decoding and
// audio belong to the platform backend; terminal pixels belong to picture.
package ntvideo

import (
	"errors"
	"image"
	"time"

	"nimbleterminal.dev/ntpicture"
)

var (
	ErrUnsupported = errors.New("ntvideo: install mpv to play video (libmpv is unavailable)")
	ErrClosed      = errors.New("ntvideo: closed")
)

type State string

const (
	Idle      State = "idle"
	Loading   State = "loading"
	Paused    State = "paused"
	Playing   State = "playing"
	Buffering State = "buffering"
	Ended     State = "ended"
	Failed    State = "failed"
	Closed    State = "closed"
)

// CellRect is relative to the visible terminal grid, with a zero-based origin.
// The containing application must keep it in sync with its composed layout.
type CellRect struct{ X, Y, Cols, Rows int }

type Caption struct {
	Source, Label, Language string
	Default                 bool
}
type Source struct {
	URL      string    // Browser URL or, on native systems, a local filename.
	Captions []Caption // WebVTT URLs in browsers; external subtitle files natively.
}
type Track struct {
	ID, Label, Language string
	Kind                string // audio, descriptions, subtitles, or captions
	Selected            bool
}
type Status struct {
	State                      State
	CurrentTime, Duration      time.Duration
	Volume                     float64 // 0..1
	Muted                      bool
	AudioTracks, CaptionTracks []Track
	Err                        error
}
type CapabilitySet struct {
	Playback, Audio, Captions, AudioTrackSelection bool
	DOM                                            bool
	Reason                                         string
}

// Capabilities reports compiled/environment support, not support for every codec
// or permission to autoplay. A successful load is still required.
func Capabilities() CapabilitySet { return platformCapabilities() }

type config struct {
	scrubber                      bool
	fit                           FitMode
	renderMode                    RenderMode
	source                        Source
	bounds                        CellRect
	volume                        float64
	muted, loop, autoplay         bool
	host, description, transcript string
	z                             int
	poster                        image.Image
	kittyMedium                   ntpicture.KittyMedium
	session                       *ntpicture.TerminalSession
	nextID                        func() (int, error)
	libraryPath                   string
	limits                        Limits
}
type Option func(*config)

func WithSource(url string) Option { return WithMedia(Source{URL: url}) }
func WithMedia(s Source) Option    { return func(c *config) { c.source = cloneSource(s) } }
func WithWidth(n int) Option       { return func(c *config) { c.bounds.Cols = n } }
func WithHeight(n int) Option      { return func(c *config) { c.bounds.Rows = n } }
func WithAutoplay(v bool) Option   { return func(c *config) { c.autoplay = v } }
func WithLoop(v bool) Option       { return func(c *config) { c.loop = v } }
func WithMuted(v bool) Option      { return func(c *config) { c.muted = v } }
func WithVolume(v float64) Option  { return func(c *config) { c.volume = volume(v) } }

// WithHost selects the registered browser terminal host. It has no native effect.
func WithHost(id string) Option         { return func(c *config) { c.host = id } }
func WithDescription(s string) Option   { return func(c *config) { c.description = s } }
func WithTranscript(url string) Option  { return func(c *config) { c.transcript = url } }
func WithPoster(img image.Image) Option { return func(c *config) { c.poster = img } }
func WithZIndex(z int) Option           { return func(c *config) { c.z = z } }

// WithKittyMedium selects how native Kitty frames are transmitted. The default is
// ntpicture.KittyMediumSharedMemory, which hands raw RGBA to a local terminal
// through a shared buffer and skips PNG encoding; picture falls back to direct
// transmission unless the terminal answers its shared-memory probe. Pass
// ntpicture.KittyMediumDirect to always stream frames through the tty.
func WithKittyMedium(m ntpicture.KittyMedium) Option { return func(c *config) { c.kittyMedium = m } }
func cloneSource(s Source) Source                    { s.Captions = append([]Caption(nil), s.Captions...); return s }
func volume(v float64) float64 {
	if v != v {
		return 1
	}
	return max(0, min(1, v))
}

// WithTerminalSession shares discovery with the host. The host initializes,
// updates and closes this session; ntvideo never closes a supplied session.
func WithTerminalSession(s *ntpicture.TerminalSession) Option {
	return func(c *config) { c.session = s }
}

// WithImageIDAllocator shares a positive 24-bit Kitty ID allocator with the host.
// The default is ntpicture.NextImageID.
func WithImageIDAllocator(f func() (int, error)) Option { return func(c *config) { c.nextID = f } }

// WithLibraryPath selects an explicit trusted libmpv shared library. Empty uses
// platform defaults. Loading happens only when media is first sampled.
func WithLibraryPath(path string) Option { return func(c *config) { c.libraryPath = path } }
func WithLimits(l Limits) Option         { return func(c *config) { c.limits = l } }
