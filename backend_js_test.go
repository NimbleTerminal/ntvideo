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
