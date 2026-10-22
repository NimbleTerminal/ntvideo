//go:build (darwin || linux || windows) && (amd64 || arm64)

package ntvideo

import (
	"image"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in integration fixture avoids network and external media in unit tests.
func TestMPVCaptionCueTimeline(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	for _, ext := range []string{".srt", ".vtt"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "captions"+ext)
			data := "1\n00:00:00,100 --> 00:00:00,150\nsame\n\n2\n00:00:00,150 --> 00:00:00,200\nsame\n"
			if ext == ".vtt" {
				data = "WEBVTT\n\n" + strings.ReplaceAll(data, ",", ".")
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			raw, err := newBackend(config{muted: true, captionEvents: true})
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if err := raw.Load(Source{URL: source, Captions: []Caption{{Source: path, Default: true}}}); err != nil {
				t.Fatal(err)
			}
			var s Status
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				s, err = raw.Poll()
				if err != nil {
					t.Fatal(err)
				}
				if s.CaptionCuesAvailable {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !s.CaptionCuesAvailable || s.CaptionCueErr != nil || s.cueData == nil {
				t.Fatalf("missing cue timeline: %+v", s)
			}
			if s.cueData.track == "" || len(s.cueData.timeline.cues) != 2 {
				t.Fatal(s.cueData)
			}
			scheduler := cueScheduler{}
			scheduler.observe(s.cueData, 0, s.Duration, CueLoad, false)
			if err := raw.Play(); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				s, err = raw.Poll()
				if err != nil {
					t.Fatal(err)
				}
				if s.CurrentTime >= 300*time.Millisecond {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			events := scheduler.observe(s.cueData, s.CurrentTime, s.Duration, "", false)
			if len(events) != 4 {
				t.Fatalf("short cues missed: time=%v events=%+v", s.CurrentTime, events)
			}
			if err := raw.SelectTrack("captions", ""); err != nil {
				t.Fatal(err)
			}
			s, err = raw.Poll()
			if err != nil {
				t.Fatal(err)
			}
			if s.CaptionCuesAvailable || s.cueData == nil || s.cueData.track != "" {
				t.Fatalf("off retained timeline: %+v", s)
			}
		})
	}
}

func TestMPVCaptionTextOverlay(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	path := filepath.Join(t.TempDir(), "captions.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:00,250 --> 00:00:01,500\nHello caption\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := newBackend(config{muted: true, nativeCaptionOverlay: true})
	if err != nil {
		t.Fatal(err)
	}
	b := raw.(*mpvBackend)
	defer b.Close()
	if err := b.Load(Source{URL: source, Captions: []Caption{{Source: path, Default: true}}}); err != nil {
		t.Fatal(err)
	}
	wait := func(at time.Duration, text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s, err := b.Poll()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Frame(160, 90); err != nil {
				t.Fatal(err)
			}
			if b.loaded && !b.seekPending && s.CurrentTime >= at && s.CaptionText == text {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		s, _ := b.Poll()
		t.Fatalf("expected cue %q at %v: %+v", text, at, s)
	}
	wait(0, "")
	if err := b.Seek(750 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	wait(700*time.Millisecond, "Hello caption")
	for _, enabled := range []bool{false, true} {
		if err := b.setCaptionOverlay(enabled); err != nil {
			t.Fatal(err)
		}
		if b.flag("sub-visibility") == enabled {
			t.Fatal("backend subtitle visibility incorrect")
		}
		wait(700*time.Millisecond, "Hello caption")
	}
	if err := b.Seek(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	wait(1900*time.Millisecond, "")
	if err := b.Seek(750 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	wait(700*time.Millisecond, "Hello caption")
	if err := b.SelectTrack("captions", ""); err != nil {
		t.Fatal(err)
	}
	wait(700*time.Millisecond, "")
}

func TestMPVCaptionTracks(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	path := filepath.Join(t.TempDir(), "captions.vtt")
	if err := os.WriteFile(path, []byte("WEBVTT\n\n00:00.000 --> 00:02.000\nCaption test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := newBackend(config{muted: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Load(Source{URL: source, Captions: []Caption{{Source: path, Label: "English", Language: "en", Default: true}, {Source: path, Label: "Spanish", Language: "es"}}}); err != nil {
		t.Fatal(err)
	}
	wait := func(want string, discover bool) Status {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s, err := b.Poll()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Frame(160, 90); err != nil {
				t.Fatal(err)
			}
			if len(s.CaptionTracks) == 2 {
				selected := ""
				for _, track := range s.CaptionTracks {
					if track.Selected {
						selected = track.ID
					}
				}
				if discover && selected != "" || !discover && selected == want {
					return s
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("caption tracks did not settle")
		return Status{}
	}
	s := wait("", true)
	if s.CaptionTracks[0].Label != "English" || s.CaptionTracks[1].Language != "es" {
		t.Fatal(s.CaptionTracks)
	}
	for _, id := range []string{s.CaptionTracks[1].ID, "", s.CaptionTracks[0].ID} {
		if err := b.SelectTrack("captions", id); err != nil {
			t.Fatal(err)
		}
		wait(id, false)
	}
}

func TestMPVPlayback(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE to a short local video")
	}
	raw, err := newBackend(config{volume: 0.5, muted: true})
	if err != nil {
		t.Fatal(err)
	}
	b := raw.(*mpvBackend)
	defer b.Close()
	if err = b.Load(Source{URL: source}); err != nil {
		t.Fatal(err)
	}
	if err = b.Play(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	var frame image.Image
	var status Status
	for time.Now().Before(deadline) {
		status, err = b.Poll()
		if err != nil {
			t.Fatal(err)
		}
		frame, err = b.Frame(160, 90)
		if err != nil {
			t.Fatal(err)
		}
		if frame != nil && status.CurrentTime > 100*time.Millisecond {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if frame == nil || status.CurrentTime <= 100*time.Millisecond || status.Duration <= 0 {
		t.Fatalf("no playback: %+v frame=%v", status, frame != nil)
	}
	if frame.Bounds().Dx() > 160 || frame.Bounds().Dy() > 90 {
		t.Fatal(frame.Bounds())
	}
	if b.aspect <= 0 || math.Abs(float64(frame.Bounds().Dx())/float64(frame.Bounds().Dy())-b.aspect) > 0.03 {
		t.Fatalf("source aspect ratio lost: frame=%v source=%v", frame.Bounds(), b.aspect)
	}
	first := frame.At(0, 0)
	different := false
	for y := 0; y < frame.Bounds().Dy(); y++ {
		for x := 0; x < frame.Bounds().Dx(); x++ {
			if frame.At(x, y) != first {
				different = true
			}
		}
	}
	if !different {
		t.Fatal("frame contains no image detail")
	}
	if err = b.Pause(); err != nil {
		t.Fatal(err)
	}
	if err = b.Seek(time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	status, err = b.Poll()
	if err != nil || status.State != Paused || status.CurrentTime < 900*time.Millisecond {
		t.Fatalf("pause/seek: %+v %v", status, err)
	}

	// A paused frame must be presented again after the surface becomes visible.
	if err = b.Layout(CellRect{}, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err = b.Layout(CellRect{}, true); err != nil {
		t.Fatal(err)
	}
	frame = nil
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// Keep processing asynchronous seek completion, as the host does.
		if _, err = b.Poll(); err != nil {
			t.Fatal(err)
		}
		frame, err = b.Frame(160, 90)
		if err != nil {
			t.Fatal(err)
		}
		if frame != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if frame == nil {
		t.Fatal("paused surface was not redrawn after becoming visible")
	}
	if err = b.SetVolume(0.3); err != nil {
		t.Fatal(err)
	}
	status, err = b.Poll()
	if err != nil || status.Volume < 0.29 || status.Volume > 0.31 || !status.Muted {
		t.Fatalf("audio control: %+v %v", status, err)
	}
}

func TestMPVInputPolicy(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	raw, err := newBackend(config{muted: true, limits: Limits{MaxWidth: 100}})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	b := raw.(*mpvBackend)
	for _, s := range []string{"https://127.0.0.1/movie", "edl://x", "fd://0"} {
		if err := b.Load(Source{URL: s}); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	if err := b.Load(Source{URL: source}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, err = b.Poll()
		if err != nil {
			if !strings.Contains(err.Error(), "dimensions") {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("resolution limit was not enforced")
}

func TestMPVDoesNotFollowNetworkPlaylist(t *testing.T) {
	if os.Getenv("NTVIDEO_TEST_FILE") == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "blocked", 404) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "network.m3u")
	if err := os.WriteFile(path, []byte("#EXTM3U\n"+server.URL+"/movie.mp4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := newBackend(config{muted: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.Load(Source{URL: path}); err != nil {
		return
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		b.Poll()
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 0 {
		t.Fatal("playlist opened a network URL")
	}
}
