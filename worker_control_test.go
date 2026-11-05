package ntvideo

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntmediabox"
)

func TestWorkerLibrarySettingsReachDecoder(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(t.TempDir(), "custom-libmpv")
	if err := os.WriteFile(lib, []byte("library"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeWorkerSettings(dir, lib); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(lib)
	if err != nil {
		t.Fatal(err)
	}
	var got config
	d := &workerDecoder{dir: dir, create: func(c config) (backend, error) { got = c; return &stubWorkerInnerBackend{}, nil }}
	defer d.Close()
	if err := d.Load(ntmediabox.OpenRequest{Name: "video.mp4"}); err != nil {
		t.Fatal(err)
	}
	if got.libraryPath != resolved || !got.disableAudio || !got.disableHW {
		t.Fatalf("worker config: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, workerSettingsName), []byte(`{"version":2,"library_path":"/fake"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.Load(ntmediabox.OpenRequest{Name: "video.mp4"}); err == nil {
		t.Fatal("invalid settings accepted")
	}
	if err := writeWorkerSettings(dir, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing library accepted")
	}
}

func TestWorkerStagingCopyFallback(t *testing.T) {
	src := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(src, []byte("video data"), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkErr, linkErr := errors.New("symlink denied"), errors.New("cross-volume hard link")
	symlink := func(string, string) error { return symlinkErr }
	hardlink := func(string, string) error { return linkErr }
	dst := filepath.Join(t.TempDir(), "video.mp4")
	if err := stageWorkerVideo(src, dst, 10, symlink, hardlink); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "video data" {
		t.Fatalf("copy: %q %v", data, err)
	}
	if st, err := os.Stat(dst); err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("copy mode: %v %v", st, err)
	}
	dst = filepath.Join(t.TempDir(), "oversized.mp4")
	err = stageWorkerVideo(src, dst, 2, symlink, hardlink)
	if !errors.Is(err, linkErr) || !errors.Is(err, symlinkErr) || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("lost staging failures: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("failed copy was retained")
	}
}

func TestWorkerMutePreferenceSettles(t *testing.T) {
	wb := &workerBackend{cfg: config{muted: true, volume: 1}}
	for _, muted := range []bool{false, true, false} {
		if err := wb.SetMuted(muted); err != nil {
			t.Fatal(err)
		}
		status, err := wb.Poll()
		if err != nil || status.Muted != muted {
			t.Fatalf("idle mute=%v status=%+v err=%v", muted, status, err)
		}
	}
	a, b := net.Pipe()
	defer b.Close()
	wb.client = ntmediabox.NewClient(a, nil, nil)
	defer wb.client.Close()
	wb.latestStatus = ntmediabox.Status{State: ntmediabox.Paused}
	for _, muted := range []bool{true, false, true} {
		_ = wb.SetMuted(muted)
		status, err := wb.Poll()
		if err != nil || status.Muted != muted {
			t.Fatalf("loaded mute=%v status=%+v err=%v", muted, status, err)
		}
	}
}

func TestWorkerCaptionSourcesSurviveMissingAndReorderedSidecars(t *testing.T) {
	dir := t.TempDir()
	inner := &stubWorkerInnerBackend{}
	d := &workerDecoder{dir: dir, create: func(config) (backend, error) { return inner, nil }}
	defer d.Close()
	if err := d.Load(ntmediabox.OpenRequest{Name: "video.mp4", Captions: []ntmediabox.Caption{{Name: "sub-0.srt"}, {Name: "sub-1.vtt"}}}); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer b.Close()
	client := ntmediabox.NewClient(a, nil, nil)
	defer client.Close()
	first := newCueTimeline([]CaptionCue{{ID: "0", End: time.Second, Text: "first"}})
	second := newCueTimeline([]CaptionCue{{ID: "0", End: time.Second, Text: "second"}})
	wb := &workerBackend{client: client, reqGen: 1, cfg: config{captionEvents: true}, sidecars: map[string]nativeCueFile{"sub-0.srt": {timeline: first}, "sub-1.vtt": {timeline: second}}}
	tests := []struct {
		name    string
		tracks  []Track
		paths   map[string]string
		want    *cueTimeline
		wantErr bool
	}{
		{"first sub-add missing", []Track{{ID: "8", Selected: true, Kind: "subtitles"}}, map[string]string{"8": filepath.Join(dir, "sub-1.vtt")}, second, false},
		{"reordered", []Track{{ID: "8", Selected: true, Kind: "subtitles"}, {ID: "9", Kind: "subtitles"}}, map[string]string{"8": filepath.Join(dir, "sub-1.vtt"), "9": filepath.Join(dir, "sub-0.srt")}, second, false},
		{"embedded with both sidecars missing", []Track{{ID: "1", Selected: true, Kind: "subtitles"}}, map[string]string{"1": ""}, nil, false},
		{"unidentified legacy helper", []Track{{ID: "1", Selected: true, Kind: "subtitles"}}, nil, nil, true},
		{"unrequested external file", []Track{{ID: "1", Selected: true, Kind: "subtitles"}}, map[string]string{"1": filepath.Join(dir, "other.srt")}, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner.mu.Lock()
			inner.status = Status{State: Paused, CaptionTracks: tc.tracks, captionFiles: tc.paths}
			inner.mu.Unlock()
			ms, err := d.Status()
			if err != nil {
				t.Fatal(err)
			}
			ms.Gen = 1
			wb.latestStatus = ms
			status, err := wb.Poll()
			if err != nil {
				t.Fatal(err)
			}
			if status.cueData == nil || status.cueData.timeline != tc.want || (status.CaptionCueErr != nil) != tc.wantErr || status.CaptionCuesAvailable != (tc.want != nil) {
				t.Fatalf("incorrect cue binding: %+v", status)
			}
			for _, track := range status.CaptionTracks {
				if track.Kind != "subtitles" {
					t.Fatalf("wire metadata leaked: %+v", track)
				}
			}
		})
	}
}

func TestWorkerUnsupportedSidecarsRemainUnsupported(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native worker backend")
	}
	for _, ext := range []string{"", ".unsupported-extension", ".ASS", ".vtt!"} {
		t.Run("extension"+ext, func(t *testing.T) {
			dir := t.TempDir()
			video, sub := filepath.Join(dir, "video.mp4"), filepath.Join(dir, "captions"+ext)
			for _, path := range []string{video, sub} {
				if err := os.WriteFile(path, []byte("fake"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			wb, err := newWorkerBackend(config{captionEvents: true, workerStart: func(_ config, dir, _ string) (io.ReadWriteCloser, func() error, error) {
				calls++
				if _, err := os.Stat(filepath.Join(dir, "sub-0"+sanitizeExt(ext))); err != nil {
					t.Fatalf("caption extension changed: %v", err)
				}
				a, b := net.Pipe()
				go serveWorker(b, dir, func(string) (ntmediabox.Decoder, error) {
					return &workerDecoder{dir: dir, create: func(config) (backend, error) { return &stubWorkerInnerBackend{}, nil }}, nil
				})
				return a, func() error { _ = b.Close(); return a.Close() }, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer wb.Close()
			if err := wb.Load(Source{URL: video, Captions: []Caption{{Source: sub}}}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("unsupported caption launched a metadata helper: starts=%d", calls)
			}
		})
	}
}

func TestWorkerHiddenResizeAndMuteDoNotResumePlayback(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native worker backend")
	}
	video := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(video, []byte("fake video"), 0600); err != nil {
		t.Fatal(err)
	}
	inner := &stubWorkerInnerBackend{}
	wb, err := newWorkerBackend(config{workerStart: func(_ config, dir, _ string) (io.ReadWriteCloser, func() error, error) {
		a, b := net.Pipe()
		go serveWorker(b, dir, func(string) (ntmediabox.Decoder, error) {
			return &workerDecoder{dir: dir, create: func(config) (backend, error) { return inner, nil }}, nil
		})
		return a, func() error { _ = b.Close(); return a.Close() }, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()
	if err := wb.Load(Source{URL: video}); err != nil {
		t.Fatal(err)
	}
	waitSize := func(w, h int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := wb.Poll(); err != nil {
				t.Fatal(err)
			}
			inner.mu.Lock()
			size := inner.lastFrameSize
			inner.mu.Unlock()
			if size == [2]int{w, h} {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("worker never rendered at %dx%d", w, h)
	}
	_, _ = wb.Frame(160, 90)
	waitSize(160, 90)
	if err := wb.Layout(CellRect{}, false); err != nil {
		t.Fatal(err)
	}
	waitSize(1, 1)
	_ = wb.SetMuted(false)
	status, err := wb.Poll()
	if err != nil || status.Muted {
		t.Fatalf("unmute never settled: %+v %v", status, err)
	}
	if err := wb.Layout(CellRect{}, true); err != nil {
		t.Fatal(err)
	}
	_, _ = wb.Frame(80, 45)
	waitSize(80, 45)
	if inner.hasOp("play") {
		t.Fatal("showing/unmuting resumed worker playback")
	}
}
