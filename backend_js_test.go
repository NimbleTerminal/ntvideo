//go:build js && wasm

package ntvideo

import (
	"strings"
	"syscall/js"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const domFixture = `
globalThis.videos=[];globalThis.surfaces=[];globalThis.revoked=[];globalThis.playRejection=null;
class Video {
 constructor(){this.style={};this.attrs={};this.children=[];this.controls=false;this.tabIndex=0;this.paused=true;this.ended=false;this.currentTime=0;this.duration=10;this.networkState=0;this.readyState=4;this.volume=1;this.muted=false;this.error=null;this.textTracks=[];}
 setAttribute(k,v){this.attrs[k]=v;}
 removeAttribute(k){delete this[k];delete this.attrs[k];}
 appendChild(t){this.children.push(t);}
 addEventListener(k,f){(this.listeners??={})[k]=f;}
 removeEventListener(k,f){if(this.listeners?.[k]===f)delete this.listeners[k];}
 load(){this.networkState=this.src?1:0;}
 pause(){this.paused=true;}
 play(){if(globalThis.playRejection)return Promise.reject(globalThis.playRejection);this.paused=false;return Promise.resolve();}
}
globalThis.document={createElement:kind=>{const v=new Video();if(kind==='video')videos.push(v);return v;}};
globalThis.URL={revokeObjectURL:url=>revoked.push(url)};
const host={mount(id,video,description,transcript,z){
 const s={id,video,closed:false,bounds:null,layout(x,y,cols,rows,visible){this.bounds={x,y,cols,rows,visible};},status(state){this.state=state;},close(){this.closed=true;}};
 surfaces.push(s);return s;
}};
globalThis.ntvideoHost=host;delete globalThis.ntchartsVideoHost;
`

func setupDOM(t *testing.T) {
	t.Helper()
	js.Global().Call("eval", domFixture)
	t.Cleanup(func() {
		js.Global().Delete("document")
		js.Global().Delete("ntvideoHost")
		js.Global().Delete("ntchartsVideoHost")
	})
}
func sampleDOM(m *Model) { m.Update(m.sample()()) }

func TestDOMCaptionCueTimeline(t *testing.T) {
	setupDOM(t)
	m := New(WithSource("movie.mp4"), WithCaptionEvents(true))
	defer m.Close()
	sampleDOM(m)
	v := domVideo(t, m)
	v.Set("textTracks", js.Global().Call("eval", `[{label:'English',language:'en',kind:'captions',mode:'showing',cues:[{startTime:0.1,endTime:0.15,text:'same'},{startTime:0.15,endTime:0.2,text:'same'},{startTime:1,endTime:2,text:'destination'}]}]`))
	sample := func() []CaptionCueMsg { return collectCueCommands(m.Update(m.sample()())) }
	sample()
	if !m.Status().CaptionCuesAvailable {
		t.Fatalf("missing timeline: %+v", m.Status())
	}
	v.Set("currentTime", 0.5)
	if events := sample(); len(events) != 4 {
		t.Fatal("missed short cues", events)
	}
	v.Get("listeners").Get("seeking").Invoke()
	v.Set("seeking", true)
	v.Set("currentTime", 1.5)
	if events := sample(); len(events) != 0 {
		t.Fatal("unsettled seek", events)
	}
	v.Set("seeking", false)
	if events := sample(); len(events) != 1 || events[0].Reason != CueSeek || events[0].Cue.Text != "destination" {
		t.Fatal(events)
	}
	if events := sample(); len(events) != 0 {
		t.Fatal("paused cue repeated", events)
	}
	m.SelectCaptionTrack("")
	if events := sample(); len(events) != 1 || events[0].Kind != CueExit || events[0].Reason != CueTrackChange {
		t.Fatal(events)
	}
	if m.Status().CaptionCuesAvailable {
		t.Fatal("off retained cue availability")
	}
	// Cue parsing failures do not turn an otherwise playable video into Failed.
	v.Set("textTracks", js.Global().Call("eval", `[{label:'Bad',language:'en',kind:'captions',mode:'showing',cues:[{startTime:2,endTime:1,text:'bad'}]}]`))
	sample()
	if m.Status().CaptionCueErr == nil || m.Status().Err != nil || m.Status().State == Failed {
		t.Fatalf("cue error affected playback: %+v", m.Status())
	}
	m.Close()
	if !v.Get("listeners").Get("seeking").IsUndefined() {
		t.Fatal("listener leaked")
	}
}

func TestDOMCaptionTracks(t *testing.T) {
	setupDOM(t)
	m := New(WithNativeControls(false), WithMedia(Source{URL: "movie.mp4", Captions: []Caption{{Source: "en.vtt", Label: "English", Language: "en", Default: true}, {Source: "es.vtt", Label: "Spanish", Language: "es"}}}))
	defer m.Close()
	sampleDOM(m)
	v := domVideo(t, m)
	children := v.Get("children")
	if children.Length() != 2 || children.Index(0).Get("src").String() != "en.vtt" || !children.Index(0).Get("default").Bool() || children.Index(1).Get("srclang").String() != "es" {
		t.Fatal("caption elements missing metadata")
	}
	// The fixture supplies the browser's resulting TextTrackList.
	v.Set("textTracks", js.Global().Call("eval", `[{label:'English',language:'en',kind:'captions',mode:'showing'},{label:'Spanish',language:'es',kind:'captions',mode:'disabled'}]`))
	for _, id := range []string{"1", "", "0"} {
		m.SelectCaptionTrack(id)
		sampleDOM(m)
		tracks := m.Status().CaptionTracks
		if len(tracks) != 2 {
			t.Fatal(tracks)
		}
		for _, track := range tracks {
			if track.Selected != (track.ID == id) {
				t.Fatalf("selection %q: %+v", id, tracks)
			}
		}
	}
	if err := m.engine.b.SelectTrack("captions", "99"); err == nil {
		t.Fatal("invalid track accepted")
	}
}
func domVideo(t *testing.T, m *Model) js.Value {
	t.Helper()
	b, ok := m.engine.b.(*domBackend)
	if !ok {
		t.Fatalf("DOM backend unavailable: %+v", m.Status())
	}
	return b.video
}
func TestDOMNativeControlsAndBridge(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "default", false: "host"}[enabled], func(t *testing.T) {
			setupDOM(t)
			opts := []Option{WithSource("blob:https://host/video"), WithDescription("Movie")}
			if !enabled {
				opts = append(opts, WithNativeControls(false))
			}
			m := New(opts...)
			defer m.Close()
			sampleDOM(m)
			v := domVideo(t, m)
			if v.Get("controls").Bool() != enabled {
				t.Fatal("wrong native controls")
			}
			if !enabled && v.Get("tabIndex").Int() != -1 {
				t.Fatal("host-driven video remains focusable")
			}
			if v.Get("attrs").Get("aria-label").String() != "Movie" {
				t.Fatal("accessible label missing")
			}
		})
	}
	setupDOM(t)
	js.Global().Set("ntchartsVideoHost", js.Global().Get("ntvideoHost"))
	js.Global().Delete("ntvideoHost")
	m := New(WithSource("clip.mp4"))
	defer m.Close()
	sampleDOM(m)
	if m.Status().Err != nil {
		t.Fatal("legacy bridge failed", m.Status().Err)
	}
}
func TestDOMSourcesAndLifecycle(t *testing.T) {
	setupDOM(t)
	m := New(WithSource("blob:https://host/video"), WithNativeControls(false), WithScrubber(true))
	defer m.Close()
	sampleDOM(m)
	v := domVideo(t, m)
	s := m.engine.b.(*domBackend).surface
	if v.Get("src").String() != "blob:https://host/video" {
		t.Fatal("blob URL changed")
	}
	m.SetBounds(CellRect{X: 3, Y: 4, Cols: 20, Rows: 6})
	sampleDOM(m)
	bounds := s.Get("bounds")
	if bounds.Get("x").Int() != 3 || bounds.Get("y").Int() != 4 || bounds.Get("cols").Int() != 20 || bounds.Get("rows").Int() != 5 {
		t.Fatal("bounds or scrubber row incorrect")
	}
	m.Play()
	sampleDOM(m)
	if m.Status().State != Playing {
		t.Fatal(m.Status())
	}
	m.SetVisible(false)
	sampleDOM(m)
	if !v.Get("paused").Bool() || s.Get("bounds").Get("visible").Bool() {
		t.Fatal("hide did not pause and hide")
	}
	m.SetVisible(true)
	sampleDOM(m)
	if !v.Get("paused").Bool() || !s.Get("bounds").Get("visible").Bool() {
		t.Fatal("show resumed or stayed hidden")
	}
	m.Load(Source{URL: "media/clip.ogg"})
	sampleDOM(m)
	if v.Get("src").String() != "media/clip.ogg" {
		t.Fatal("relative URL changed")
	}
	m.Close()
	m.Close()
	if !s.Get("closed").Bool() || !v.Get("src").IsUndefined() || !v.Get("paused").Bool() {
		t.Fatal("close left media attached")
	}
	if js.Global().Get("revoked").Length() != 0 {
		t.Fatal("host-owned URL revoked")
	}
}
func TestDOMStatusAndScrubber(t *testing.T) {
	setupDOM(t)
	m := New(WithSource("clip.mp4"), WithNativeControls(false), WithScrubber(true), WithWidth(50), WithHeight(6))
	defer m.Close()
	sampleDOM(m)
	v := domVideo(t, m)
	v.Set("currentTime", 2)
	v.Set("duration", 10)
	sampleDOM(m)
	if m.Position() != 2*time.Second || m.Duration() != 10*time.Second || m.Status().State != Paused {
		t.Fatal(m.Status())
	}
	if len(strings.Split(m.View(), "\n")) != 6 || !strings.Contains(m.View(), "0:02 / 0:10") {
		t.Fatal("DOM scrubber missing", m.View())
	}
	m.Play()
	sampleDOM(m)
	if m.Status().State != Playing {
		t.Fatal(m.Status())
	}
	m.Pause()
	sampleDOM(m)
	if m.Status().State != Paused {
		t.Fatal(m.Status())
	}
	n, _ := m.scrubberParts()
	m.Update(tea.MouseClickMsg{X: 1, Y: 5, Button: tea.MouseLeft})
	sampleDOM(m)
	if v.Get("currentTime").Float() != 0 {
		t.Fatal("scrubber click did not seek")
	}
	m.Update(tea.MouseMotionMsg{X: n, Y: 5, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: n, Y: 5, Button: tea.MouseLeft})
	sampleDOM(m)
	if v.Get("currentTime").Float() != 10 {
		t.Fatal("scrubber drag did not seek")
	}
	v.Set("ended", true)
	sampleDOM(m)
	if m.Status().State != Ended {
		t.Fatal(m.Status())
	}
}
func TestDOMAutoplayRejection(t *testing.T) {
	setupDOM(t)
	js.Global().Set("playRejection", js.Global().Get("Error").New("autoplay denied"))
	m := New(WithSource("clip.mp4"), WithAutoplay(true))
	defer m.Close()
	sampleDOM(m)
	deadline := time.Now().Add(time.Second)
	for m.Status().Err == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		sampleDOM(m)
	}
	if m.Status().State != Failed || m.Status().Err == nil || !strings.Contains(m.Status().Err.Error(), "autoplay denied") {
		t.Fatal("rejected play was silent", m.Status())
	}
}
