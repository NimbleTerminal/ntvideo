//go:build (darwin || linux || windows) && (amd64 || arm64)

package ntvideo

import (
	"errors"
	"fmt"
	"image"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

type mpvBackend struct {
	handle           unsafe.Pointer
	lib              *mpvLibrary
	cfg              config
	stop, done       chan struct{}
	mu               sync.Mutex // Protects only frame exchange; renderer never waits on control APIs.
	width, height    int
	latest           image.Image
	renderErr        error
	redraw           bool
	loading, loaded  bool
	seekPending      bool
	aspect           float64 // Control-thread metadata; dimensions sent to renderer under mu.
	captions         []Caption
	tracksAt         time.Time
	audio, subtitles []Track
	cueFiles         map[string]nativeCueFile
	cueSeekSerial    uint64
}

func platformCapabilities() CapabilitySet {
	return CapabilitySet{Playback: true, Audio: true, Captions: true, AudioTrackSelection: true}
}
func newBackend(c config) (backend, error) {
	lib, err := openMPV(c.libraryPath)
	if err != nil {
		return nil, err
	}
	var handle unsafe.Pointer
	lib.fn("mpv_create").call(unsafe.Pointer(&handle))
	b := &mpvBackend{lib: lib, handle: handle, cfg: c, stop: make(chan struct{}), done: make(chan struct{}), width: 480, height: 320}
	if b.handle == nil {
		lib.close()
		return nil, errors.New("ntvideo: could not allocate media backend")
	}
	// Sources are opened by libmpv alone: no user config, no Lua scripts, and no
	// youtube-dl hook, which would otherwise run an external program for URLs.
	// Without an audio device, play silently instead of failing.
	opts := [][2]string{{"config", "no"}, {"input-default-bindings", "no"}, {"input-vo-keyboard", "no"}, {"osc", "no"}, {"access-references", "no"}, {"demuxer-lavf-o", "protocol_whitelist=file"}, {"stream-lavf-o", "protocol_whitelist=file"}, {"sub-auto", "no"}, {"audio-file-auto", "no"}, {"terminal", "no"}, {"load-scripts", "no"}, {"ytdl", "no"}, {"vo", "libmpv"}, {"hwdec", "auto-copy"}, {"keep-open", "yes"}, {"idle", "yes"}, {"pause", "yes"}, {"audio-display", "no"}, {"osd-level", "0"}, {"audio-fallback-to-null", "yes"}}
	if c.nativeCaptionOverlay {
		opts = append(opts, [2]string{"sub-visibility", "no"})
	}
	for _, o := range opts {
		if err := b.option(o[0], o[1]); err != nil {
			b.destroy()
			return nil, err
		}
	}
	if err := b.lib.err(b.lib.fn("mpv_initialize").integer(unsafe.Pointer(&b.handle))); err != nil {
		b.destroy()
		return nil, err
	}
	ready := make(chan error, 1)
	go b.renderLoop(ready)
	if err := <-ready; err != nil {
		b.destroy()
		return nil, err
	}
	if err := b.SetVolume(c.volume); err != nil {
		b.Close()
		return nil, err
	}
	if err := b.SetMuted(c.muted); err != nil {
		b.Close()
		return nil, err
	}
	if c.loop {
		if err := b.property("loop-file", "inf"); err != nil {
			b.Close()
			return nil, err
		}
	}
	return b, nil
}
func (b *mpvBackend) destroy() {
	b.lib.fn("mpv_terminate_destroy").call(nil, unsafe.Pointer(&b.handle))
	b.lib.close()
}
func (b *mpvBackend) set(fn, name, value string) error {
	n, v := cstring(name), cstring(value)
	return b.lib.err(b.lib.fn(fn).integer(unsafe.Pointer(&b.handle), unsafe.Pointer(&n), unsafe.Pointer(&v)))
}
func (b *mpvBackend) option(name, value string) error {
	return b.set("mpv_set_option_string", name, value)
}
func (b *mpvBackend) property(name, value string) error {
	return b.set("mpv_set_property_string", name, value)
}
func (b *mpvBackend) command(a, arg, c string, extra ...string) error {
	values := []string{a, arg}
	if c != "" || len(extra) > 0 {
		values = append(values, c)
	}
	values = append(values, extra...)
	args := make([]*byte, len(values)+1)
	for i, value := range values {
		if strings.ContainsRune(value, 0) {
			return errors.New("ntvideo: invalid source or track")
		}
		args[i] = cstring(value)
	}
	ptr := unsafe.Pointer(&args[0])
	code := b.lib.fn("mpv_command").integer(unsafe.Pointer(&b.handle), unsafe.Pointer(&ptr))
	runtime.KeepAlive(args)
	return b.lib.err(code)
}
func (b *mpvBackend) number(name string) float64 {
	n := cstring(name)
	format := int32(5)
	var value float64
	ptr := unsafe.Pointer(&value)
	b.lib.fn("mpv_get_property").integer(unsafe.Pointer(&b.handle), unsafe.Pointer(&n), unsafe.Pointer(&format), unsafe.Pointer(&ptr))
	return value
}
func (b *mpvBackend) flag(name string) bool {
	n := cstring(name)
	format := int32(3)
	var value int32
	ptr := unsafe.Pointer(&value)
	b.lib.fn("mpv_get_property").integer(unsafe.Pointer(&b.handle), unsafe.Pointer(&n), unsafe.Pointer(&format), unsafe.Pointer(&ptr))
	return value != 0
}
func (b *mpvBackend) text(name string) string {
	n := cstring(name)
	var value unsafe.Pointer
	b.lib.fn("mpv_get_property_string").call(unsafe.Pointer(&value), unsafe.Pointer(&b.handle), unsafe.Pointer(&n))
	if value == nil {
		return ""
	}
	defer b.lib.fn("mpv_free").call(nil, unsafe.Pointer(&value))
	return goString(value)
}
func (b *mpvBackend) Load(s Source) error {
	s = cloneSource(s)
	path, err := validateLocalFile(s.URL, b.cfg.limits)
	if err != nil {
		return err
	}
	s.URL = path
	files := map[string]nativeCueFile{}
	for i := range s.Captions {
		path, err := validateLocalFile(s.Captions[i].Source, b.cfg.limits)
		if err != nil {
			return err
		}
		s.Captions[i].Source = path
		if b.cfg.captionEvents {
			// Bound aggregate parser memory as well as each sidecar.
			if len(files) >= 16 {
				files[path] = nativeCueFile{err: errors.New("ntvideo: cue events support at most 16 sidecars")}
				continue
			}
			files[path] = readCueFile(path)
		}
	}
	b.cueFiles = files
	b.loading = true
	b.loaded = false
	b.seekPending = false
	b.clearFrame()
	b.aspect = 0
	b.captions = s.Captions
	b.audio = nil
	b.subtitles = nil
	if err := b.command("loadfile", s.URL, "replace"); err != nil {
		return err
	}
	if b.cfg.autoplay {
		return b.Play()
	}
	return b.Pause()
}
func (b *mpvBackend) Play() error  { return b.property("pause", "no") }
func (b *mpvBackend) Pause() error { return b.property("pause", "yes") }
func (b *mpvBackend) Seek(t time.Duration) error {
	if err := b.command("seek", strconv.FormatFloat(t.Seconds(), 'f', 6, 64), "absolute+exact"); err != nil {
		return err
	}
	b.seekPending = true
	b.clearFrame()
	return nil
}
func (b *mpvBackend) SetVolume(v float64) error {
	return b.property("volume", strconv.FormatFloat(v*100, 'f', 3, 64))
}
func (b *mpvBackend) SetMuted(v bool) error {
	s := "no"
	if v {
		s = "yes"
	}
	return b.property("mute", s)
}
func (b *mpvBackend) SelectTrack(kind, id string) error {
	name := "sid"
	if kind == "audio" {
		name = "aid"
	}
	if id == "" {
		id = "no"
	} else if _, err := strconv.Atoi(id); err != nil {
		return errors.New("ntvideo: unknown track")
	}
	b.tracksAt = time.Time{}
	return b.property(name, id)
}
func (b *mpvBackend) setCaptionOverlay(enabled bool) error {
	visibility := "yes"
	if enabled {
		visibility = "no"
	}
	if err := b.property("sub-visibility", visibility); err != nil {
		return err
	}
	b.clearFrame()
	return nil
}
func (b *mpvBackend) Layout(_ CellRect, visible bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.redraw = true
	if !visible {
		b.width, b.height = 1, 1
		b.latest = nil
	}
	return nil
}
func (*mpvBackend) SetFit(FitMode) error { return nil } // picture owns native fitting.
func (b *mpvBackend) Poll() (Status, error) {
	var eventErr error
	for i := 0; i < 256; i++ {
		var event unsafe.Pointer
		timeout := float64(0)
		b.lib.fn("mpv_wait_event").call(unsafe.Pointer(&event), unsafe.Pointer(&b.handle), unsafe.Pointer(&timeout))
		if event == nil {
			break
		}
		ev := *(*mpvEvent)(event)
		if ev.ID == 0 {
			break
		}
		if ev.ID == 20 && !b.cfg.loop {
			b.cueSeekSerial++
		} // Model.Seek marks explicit seeks; automatic native loops remain playback.
		code := ev.Error
		reason := int32(-1)
		if ev.ID == 7 && ev.Data != nil {
			end := *(*mpvEndFile)(ev.Data)
			code = end.Error
			reason = end.Reason
		}
		if e := b.lib.err(code); e != nil {
			eventErr = e
		}
		if ev.ID == 7 {
			b.loading = false
			if reason != 0 {
				b.loaded = false // Stopped or failed; keep-open retains EOF frames.
			}
		}
		if ev.ID == 21 { // MPV_EVENT_PLAYBACK_RESTART: a seek/load has settled.
			b.seekPending = false
			b.clearFrame()
		}
		if ev.ID == 8 {
			b.loading = false
			b.loaded = true
			b.clearFrame()
			b.tracksAt = time.Time{}
			for _, c := range b.captions {
				mode := "auto"
				if c.Default {
					mode = "select"
				}
				if err := b.command("sub-add", c.Source, mode, c.Label, c.Language); err != nil {
					eventErr = err
				}
			}
			b.captions = nil
		}
	}
	if b.loaded {
		if err := checkDimensions(int(b.number("width")), int(b.number("height")), b.cfg.limits); err != nil {
			_ = b.command("stop", "", "")
			b.loaded = false
			b.clearFrame()
			return Status{State: Failed}, err
		}
	}
	s := Status{State: Idle, CurrentTime: time.Duration(b.number("time-pos") * float64(time.Second)), Duration: time.Duration(b.number("duration") * float64(time.Second)), Volume: b.number("volume") / 100, Muted: b.flag("mute")}
	if b.loading {
		s.State = Loading
	}
	if b.loaded {
		s.State = Playing
		if b.flag("pause") {
			s.State = Paused
		}
		if b.flag("paused-for-cache") {
			s.State = Buffering
		}
		if b.flag("eof-reached") {
			s.State = Ended
		}
	}
	if time.Since(b.tracksAt) > time.Second {
		b.audio = nil
		b.subtitles = nil
		for i, n := 0, min(128, int(b.number("track-list/count"))); i < n; i++ {
			p := fmt.Sprintf("track-list/%d/", i)
			kind := b.text(p + "type")
			t := Track{ID: b.text(p + "id"), Label: b.text(p + "title"), Language: b.text(p + "lang"), Selected: b.flag(p + "selected"), Kind: kind}
			if kind == "audio" {
				if b.flag(p + "visual-impaired") {
					t.Kind = "descriptions"
				}
				b.audio = append(b.audio, t)
			} else if kind == "sub" {
				t.Kind = "subtitles"
				b.subtitles = append(b.subtitles, t)
			}
		}
		b.tracksAt = time.Now()
	}
	// Render at the display aspect ratio rather than baking letterbox bars into
	// the frame. picture can then contain, cover, or stretch the actual video.
	dw, dh := b.number("video-out-params/dw"), b.number("video-out-params/dh")
	if dw > 0 && dh > 0 {
		b.aspect = dw / dh
	}
	s.AudioTracks = append([]Track(nil), b.audio...)
	s.CaptionTracks = append([]Track(nil), b.subtitles...)
	b.captionSnapshot(&s)
	if b.loaded && !b.seekPending {
		s.CaptionText = captionPlainText(b.text("sub-text"))
	}
	b.mu.Lock()
	renderErr := b.renderErr
	b.renderErr = nil
	b.mu.Unlock()
	if renderErr != nil {
		eventErr = renderErr
	}
	return s, eventErr
}
func (b *mpvBackend) clearFrame() {
	b.mu.Lock()
	b.latest = nil
	b.redraw = true
	b.mu.Unlock()
}
func (b *mpvBackend) Frame(w, h int) (image.Image, error) {
	if !b.loaded || b.seekPending {
		return nil, nil
	}
	if b.aspect > 0 && !math.IsInf(b.aspect, 0) && !math.IsNaN(b.aspect) {
		if float64(w)/float64(h) > b.aspect {
			w = max(1, int(math.Round(float64(h)*b.aspect)))
		} else {
			h = max(1, int(math.Round(float64(w)/b.aspect)))
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.width != w || b.height != h {
		b.latest = nil
	}
	b.width = w
	b.height = h
	img := b.latest
	b.latest = nil
	return img, nil
}
func (b *mpvBackend) renderLoop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)
	var renderer unsafe.Pointer
	rendererPtr := unsafe.Pointer(&renderer)
	api := cstring("sw")
	params := []mpvRenderParam{{Kind: 1, Data: unsafe.Pointer(api)}, {}}
	paramsPtr := unsafe.Pointer(&params[0])
	code := b.lib.fn("mpv_render_context_create").integer(unsafe.Pointer(&rendererPtr), unsafe.Pointer(&b.handle), unsafe.Pointer(&paramsPtr))
	runtime.KeepAlive(params)
	runtime.KeepAlive(api)
	if code < 0 {
		ready <- b.lib.err(code)
		return
	}
	defer b.lib.fn("mpv_render_context_free").call(nil, unsafe.Pointer(&renderer))
	ready <- nil
	ticker := time.NewTicker(time.Second / 30)
	defer ticker.Stop()
	oldW, oldH := 0, 0
	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			b.mu.Lock()
			w, h, redraw := b.width, b.height, b.redraw
			b.redraw = false
			b.mu.Unlock()
			var flags uint64
			b.lib.fn("mpv_render_context_update").call(unsafe.Pointer(&flags), unsafe.Pointer(&renderer))
			if flags&1 == 0 && !redraw && w == oldW && h == oldH {
				continue
			}
			oldW, oldH = w, h
			data := make([]byte, w*h*4)
			size := [2]int32{int32(w), int32(h)}
			stride := uintptr(w * 4)
			format := cstring("rgb0")
			block := int32(0)
			params := []mpvRenderParam{
				{Kind: 17, Data: unsafe.Pointer(&size[0])},
				{Kind: 18, Data: unsafe.Pointer(format)},
				{Kind: 19, Data: unsafe.Pointer(&stride)},
				{Kind: 20, Data: unsafe.Pointer(&data[0])},
				{Kind: 12, Data: unsafe.Pointer(&block)}, {},
			}
			pp := unsafe.Pointer(&params[0])
			code := b.lib.fn("mpv_render_context_render").integer(unsafe.Pointer(&renderer), unsafe.Pointer(&pp))
			runtime.KeepAlive(params)
			runtime.KeepAlive(data)
			runtime.KeepAlive(format)
			if code < 0 {
				b.mu.Lock()
				b.renderErr = b.lib.err(code)
				b.mu.Unlock()
				continue
			}
			b.lib.fn("mpv_render_context_report_swap").call(nil, unsafe.Pointer(&renderer))
			for i := 3; i < len(data); i += 4 {
				data[i] = 255
			}
			img := &image.NRGBA{Pix: data, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
			b.mu.Lock()
			if b.width == w && b.height == h {
				b.latest = img
			}
			b.mu.Unlock()
		}
	}
}

func (b *mpvBackend) Close() error {
	close(b.stop)
	<-b.done
	b.destroy()
	return nil
}
