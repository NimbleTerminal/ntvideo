//go:build libmpv && cgo && !js

package ntvideo

/*
#cgo pkg-config: mpv
#include <mpv/client.h>
#include <mpv/render.h>
#include <stdlib.h>
#include <stdatomic.h>

typedef struct { mpv_render_context *ctx; atomic_int dirty; } video_renderer;
static void video_updated(void *p) { atomic_store(&((video_renderer*)p)->dirty, 1); }
static video_renderer *video_renderer_new(mpv_handle *h, int *error) {
 video_renderer *r = calloc(1,sizeof(*r));
 if (!r) { *error=MPV_ERROR_NOMEM; return NULL; }
 atomic_init(&r->dirty,1);
 mpv_render_param params[]={{MPV_RENDER_PARAM_API_TYPE,MPV_RENDER_API_TYPE_SW},{0,NULL}};
 *error=mpv_render_context_create(&r->ctx,h,params);
 if (*error<0) {free(r);return NULL;}
 mpv_render_context_set_update_callback(r->ctx,video_updated,r);
 return r;
}
static int video_render(video_renderer *r, void *pixels, int w,int h,int force) {
 int dirty=atomic_exchange(&r->dirty,0);
 if (!dirty && !force) return 0;
 mpv_render_context_update(r->ctx);
 int size[]={w,h}; size_t stride=(size_t)w*4;
 mpv_render_param params[]={
  {MPV_RENDER_PARAM_SW_SIZE,size},{MPV_RENDER_PARAM_SW_FORMAT,"rgb0"},
  {MPV_RENDER_PARAM_SW_STRIDE,&stride},{MPV_RENDER_PARAM_SW_POINTER,pixels},{0,NULL}};
 int result=mpv_render_context_render(r->ctx,params);
 if(result<0)return result;
 mpv_render_context_report_swap(r->ctx);
 return 1;
}
static void video_renderer_free(video_renderer *r) {
 mpv_render_context_set_update_callback(r->ctx,NULL,NULL);
 mpv_render_context_free(r->ctx);free(r);
}
static int video_command(mpv_handle *h,const char *a,const char *b,const char *c) {
 const char *args[]={a,b,c,NULL};return mpv_command(h,args);
}
static double video_number(mpv_handle *h,const char *name) {
 double value=0;mpv_get_property(h,name,MPV_FORMAT_DOUBLE,&value);return value;
}
static int video_flag(mpv_handle *h,const char *name) {
 int value=0;mpv_get_property(h,name,MPV_FORMAT_FLAG,&value);return value;
}
static int video_event_error(mpv_event *ev) {
 if(ev->event_id==MPV_EVENT_END_FILE && ev->data) return ((mpv_event_end_file*)ev->data)->error;
 return ev->error;
}
static int video_event_end_reason(mpv_event *ev) {
 if(ev->event_id==MPV_EVENT_END_FILE && ev->data) return ((mpv_event_end_file*)ev->data)->reason;
 return -1;
}
*/
import "C"

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
	handle           *C.mpv_handle
	cfg              config
	stop, done       chan struct{}
	mu               sync.Mutex // Protects only frame exchange; renderer never waits on control APIs.
	width, height    int
	latest           image.Image
	renderErr        error
	redraw           bool
	loading, loaded  bool
	aspect           float64 // Control-thread metadata; dimensions sent to renderer under mu.
	captions         []Caption
	tracksAt         time.Time
	audio, subtitles []Track
}

func platformCapabilities() CapabilitySet {
	return CapabilitySet{Playback: true, Audio: true, Captions: true, AudioTrackSelection: true}
}
func mpvError(code C.int) error {
	if code >= 0 {
		return nil
	}
	return fmt.Errorf("ntvideo: media backend: %s", C.GoString(C.mpv_error_string(code)))
}
func newBackend(c config) (backend, error) {
	b := &mpvBackend{handle: C.mpv_create(), cfg: c, stop: make(chan struct{}), done: make(chan struct{}), width: 480, height: 320}
	if b.handle == nil {
		return nil, errors.New("ntvideo: could not allocate media backend")
	}
	// Sources are opened by libmpv alone: no user config, no Lua scripts, and no
	// youtube-dl hook, which would otherwise run an external program for URLs.
	// Without an audio device, play silently instead of failing.
	opts := [][2]string{{"config", "no"}, {"terminal", "no"}, {"load-scripts", "no"}, {"ytdl", "no"}, {"vo", "libmpv"}, {"hwdec", "auto-copy"}, {"keep-open", "yes"}, {"idle", "yes"}, {"pause", "yes"}, {"audio-display", "no"}, {"osd-level", "0"}, {"audio-fallback-to-null", "yes"}}
	for _, o := range opts {
		if err := b.option(o[0], o[1]); err != nil {
			C.mpv_terminate_destroy(b.handle)
			return nil, err
		}
	}
	if err := mpvError(C.mpv_initialize(b.handle)); err != nil {
		C.mpv_terminate_destroy(b.handle)
		return nil, err
	}
	ready := make(chan error, 1)
	go b.renderLoop(ready)
	if err := <-ready; err != nil {
		C.mpv_terminate_destroy(b.handle)
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
func (b *mpvBackend) option(name, value string) error {
	n, v := C.CString(name), C.CString(value)
	defer C.free(unsafe.Pointer(n))
	defer C.free(unsafe.Pointer(v))
	return mpvError(C.mpv_set_option_string(b.handle, n, v))
}
func (b *mpvBackend) property(name, value string) error {
	n, v := C.CString(name), C.CString(value)
	defer C.free(unsafe.Pointer(n))
	defer C.free(unsafe.Pointer(v))
	return mpvError(C.mpv_set_property_string(b.handle, n, v))
}
func (b *mpvBackend) command(a, bb, c string) error {
	if strings.IndexByte(bb, 0) >= 0 || strings.IndexByte(c, 0) >= 0 {
		return errors.New("ntvideo: invalid source or track")
	}
	ca, cb := C.CString(a), C.CString(bb)
	defer C.free(unsafe.Pointer(ca))
	defer C.free(unsafe.Pointer(cb))
	var cc *C.char
	if c != "" {
		cc = C.CString(c)
		defer C.free(unsafe.Pointer(cc))
	}
	return mpvError(C.video_command(b.handle, ca, cb, cc))
}
func (b *mpvBackend) number(name string) float64 {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	return float64(C.video_number(b.handle, n))
}
func (b *mpvBackend) flag(name string) bool {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	return C.video_flag(b.handle, n) != 0
}
func (b *mpvBackend) text(name string) string {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	s := C.mpv_get_property_string(b.handle, n)
	if s == nil {
		return ""
	}
	defer C.mpv_free(unsafe.Pointer(s))
	return C.GoString(s)
}
func (b *mpvBackend) Load(s Source) error {
	b.loading = true
	b.loaded = false
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
	return b.command("seek", strconv.FormatFloat(t.Seconds(), 'f', 6, 64), "absolute+exact")
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
		ev := C.mpv_wait_event(b.handle, 0)
		if ev.event_id == C.MPV_EVENT_NONE {
			break
		}
		if e := mpvError(C.video_event_error(ev)); e != nil {
			eventErr = e
		}
		if ev.event_id == C.MPV_EVENT_END_FILE {
			b.loading = false
			if C.video_event_end_reason(ev) != C.MPV_END_FILE_REASON_EOF {
				b.loaded = false // Stopped or failed; keep-open retains EOF frames.
			}
		}
		if ev.event_id == C.MPV_EVENT_FILE_LOADED {
			b.loading = false
			b.loaded = true
			b.tracksAt = time.Time{}
			for _, c := range b.captions {
				mode := "auto"
				if c.Default {
					mode = "select"
				}
				if err := b.command("sub-add", c.Source, mode); err != nil {
					eventErr = err
				}
			}
			b.captions = nil
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
	b.mu.Lock()
	renderErr := b.renderErr
	b.renderErr = nil
	b.mu.Unlock()
	if renderErr != nil {
		eventErr = renderErr
	}
	return s, eventErr
}
func (b *mpvBackend) Frame(w, h int) (image.Image, error) {
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
	var code C.int
	r := C.video_renderer_new(b.handle, &code)
	if r == nil {
		ready <- mpvError(code)
		return
	}
	defer C.video_renderer_free(r)
	ready <- nil
	ticker := time.NewTicker(time.Second / 30)
	defer ticker.Stop()
	var pixels unsafe.Pointer
	defer func() { C.free(pixels) }()
	oldW, oldH := 0, 0
	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			b.mu.Lock()
			w, h := b.width, b.height
			redraw := b.redraw
			b.redraw = false
			b.mu.Unlock()
			force := C.int(0)
			if redraw {
				force = 1
			}
			if w != oldW || h != oldH {
				C.free(pixels)
				pixels = C.malloc(C.size_t(w * h * 4))
				oldW, oldH = w, h
				force = 1
			}
			if pixels == nil {
				b.mu.Lock()
				b.renderErr = errors.New("ntvideo: frame allocation failed")
				b.mu.Unlock()
				return
			}
			result := C.video_render(r, pixels, C.int(w), C.int(h), force)
			if result < 0 {
				b.mu.Lock()
				b.renderErr = mpvError(result)
				b.mu.Unlock()
				continue
			}
			if result == 0 {
				continue
			}
			data := C.GoBytes(pixels, C.int(w*h*4))
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
	C.mpv_terminate_destroy(b.handle)
	return nil
}
