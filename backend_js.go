//go:build js && wasm

package ntvideo

import (
	"errors"
	"fmt"
	"image"
	"strconv"
	"sync"
	"syscall/js"
	"time"
)

type domBackend struct {
	cfg            config
	video, surface js.Value
	mu             sync.Mutex
	asyncErr       error
	closed         bool
	cueCache       domCueCache
	cueSeekSerial  uint64
	cueSeeking     js.Func
	cueListening   bool
}

// Prefer the current bridge; older host pages can keep the legacy global.
func browserHost() js.Value {
	host := js.Global().Get("ntvideoHost")
	if host.IsUndefined() || host.IsNull() {
		host = js.Global().Get("ntchartsVideoHost")
	}
	return host
}

func platformCapabilities() CapabilitySet {
	if js.Global().Get("document").IsUndefined() {
		return CapabilitySet{DOM: true, Reason: "ntvideo: browser document unavailable"}
	}
	host := browserHost()
	if host.IsUndefined() || host.IsNull() {
		return CapabilitySet{DOM: true, Reason: "ntvideo: load web/surface.js and register a terminal host"}
	}
	v := js.Global().Get("document").Call("createElement", "video")
	return CapabilitySet{DOM: true, Playback: true, Audio: true, Captions: true, AudioTrackSelection: !v.Get("audioTracks").IsUndefined()}
}
func jsTry(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ntvideo: browser operation: %v", r)
		}
	}()
	f()
	return
}
func newBackend(c config) (b backend, err error) {
	caps := platformCapabilities()
	if !caps.Playback {
		return nil, errors.New(caps.Reason)
	}
	d := &domBackend{cfg: c}
	err = jsTry(func() {
		d.video = js.Global().Get("document").Call("createElement", "video")
		v := d.video
		v.Set("controls", c.nativeControls)
		if !c.nativeControls {
			v.Set("tabIndex", -1)
		}
		v.Set("playsInline", true)
		v.Set("preload", "metadata")
		v.Set("loop", c.loop)
		v.Set("muted", c.muted)
		v.Set("volume", c.volume)
		v.Call("setAttribute", "aria-label", c.description)
		d.surface = browserHost().Call("mount", c.host, v, c.description, c.transcript, c.z)
	})
	if err != nil {
		return nil, err
	}
	if err = d.Layout(mediaBounds(c), true); err != nil {
		d.Close()
		return nil, err
	}
	if err = d.SetFit(c.fit); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
func (b *domBackend) Load(s Source) error {
	return jsTry(func() {
		b.cueCache = domCueCache{}
		if b.cfg.captionEvents && !b.cueListening {
			b.cueSeeking = js.FuncOf(func(js.Value, []js.Value) any { b.cueSeekSerial++; return nil })
			b.video.Call("addEventListener", "seeking", b.cueSeeking)
			b.cueListening = true
		}
		b.video.Call("pause")
		b.video.Set("textContent", "")
		for _, c := range s.Captions {
			t := js.Global().Get("document").Call("createElement", "track")
			t.Set("kind", "captions")
			t.Set("src", c.Source)
			t.Set("label", c.Label)
			t.Set("srclang", c.Language)
			t.Set("default", c.Default)
			b.video.Call("appendChild", t)
		}
		b.video.Set("src", s.URL)
		b.video.Call("load")
		if b.cfg.autoplay {
			b.play()
		}
	})
}
func (b *domBackend) play() {
	p := b.video.Call("play")
	var ok, bad js.Func
	release := func() { ok.Release(); bad.Release() }
	ok = js.FuncOf(func(js.Value, []js.Value) any { release(); return nil })
	bad = js.FuncOf(func(_ js.Value, args []js.Value) any {
		b.mu.Lock()
		if !b.closed {
			b.asyncErr = fmt.Errorf("ntvideo: play rejected: %s", args[0].Get("message").String())
		}
		b.mu.Unlock()
		release()
		return nil
	})
	p.Call("then", ok, bad)
}
func (b *domBackend) Play() error  { return jsTry(b.play) }
func (b *domBackend) Pause() error { return jsTry(func() { b.video.Call("pause") }) }
func (b *domBackend) Seek(t time.Duration) error {
	return jsTry(func() { b.video.Set("currentTime", t.Seconds()) })
}
func (b *domBackend) SetVolume(v float64) error { return jsTry(func() { b.video.Set("volume", v) }) }
func (b *domBackend) SetMuted(v bool) error     { return jsTry(func() { b.video.Set("muted", v) }) }
func (b *domBackend) Layout(r CellRect, visible bool) error {
	return jsTry(func() { b.surface.Call("layout", r.X, r.Y, r.Cols, r.Rows, visible) })
}
func (b *domBackend) SelectTrack(kind, id string) error {
	list := b.video.Get("textTracks")
	property := "mode"
	if kind == "audio" {
		list = b.video.Get("audioTracks")
		property = "enabled"
	}
	if list.IsUndefined() {
		return errors.New("ntvideo: audio track selection unavailable in this browser")
	}
	index := -1
	if id != "" {
		n, e := strconv.Atoi(id)
		if e != nil || n < 0 || n >= list.Length() {
			return errors.New("ntvideo: unknown track")
		}
		index = n
	}
	return jsTry(func() {
		for i := 0; i < list.Length(); i++ {
			if property == "mode" {
				mode := "disabled"
				if i == index {
					mode = "showing"
				}
				list.Index(i).Set(property, mode)
			} else {
				list.Index(i).Set(property, i == index)
			}
		}
	})
}
func browserDuration(v js.Value) time.Duration {
	return mediaDuration(v.Float())
}
func (b *domBackend) Poll() (s Status, err error) {
	err = jsTry(func() {
		v := b.video
		s = Status{State: Paused, Volume: v.Get("volume").Float(), Muted: v.Get("muted").Bool(), CurrentTime: browserDuration(v.Get("currentTime")), Duration: browserDuration(v.Get("duration"))}
		if v.Get("networkState").Int() == 0 { // NETWORK_EMPTY: no source assigned.
			s.State = Idle
		} else if v.Get("readyState").Int() == 0 {
			s.State = Loading
		} else if v.Get("ended").Bool() {
			s.State = Ended
		} else if !v.Get("paused").Bool() {
			s.State = Playing
			if v.Get("readyState").Int() < 3 {
				s.State = Buffering
			}
		}
		if e := v.Get("error"); !e.IsNull() && !e.IsUndefined() {
			s.Err = fmt.Errorf("ntvideo: media error %d: %s", e.Get("code").Int(), e.Get("message").String())
			s.State = Failed
		}
		for _, kind := range []string{"textTracks", "audioTracks"} {
			list := v.Get(kind)
			if list.IsUndefined() {
				continue
			}
			for i := 0; i < list.Length(); i++ {
				t := list.Index(i)
				track := Track{ID: strconv.Itoa(i), Label: t.Get("label").String(), Language: t.Get("language").String(), Kind: t.Get("kind").String()}
				if kind == "textTracks" {
					track.Selected = t.Get("mode").String() == "showing"
					s.CaptionTracks = append(s.CaptionTracks, track)
				} else {
					track.Selected = t.Get("enabled").Bool()
					s.AudioTracks = append(s.AudioTracks, track)
				}
			}
		}
		b.captionSnapshot(&s)
		b.surface.Call("status", string(s.State))
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.asyncErr != nil {
		err = b.asyncErr
		b.asyncErr = nil
	}
	return
}
func (*domBackend) Frame(int, int) (image.Image, error) { return nil, nil }
func (b *domBackend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	return jsTry(func() {
		if b.cueListening {
			defer b.cueSeeking.Release()
			b.cueListening = false
			b.video.Call("removeEventListener", "seeking", b.cueSeeking)
		}
		b.video.Call("pause")
		b.video.Call("removeAttribute", "src")
		b.video.Set("textContent", "")
		b.video.Call("load")
		b.surface.Call("close")
	})
}

func (b *domBackend) SetFit(f FitMode) error {
	return jsTry(func() { b.video.Get("style").Set("objectFit", f.String()) })
}
