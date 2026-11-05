package ntvideo

import (
	"image"
	"image/color"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nimbleterminal.dev/ntmediabox"
)

const testWorkerArg = "--ntvideo-test-worker"

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == testWorkerArg {
		if err := ServeWorker(ntmediabox.WorkerConn(), os.Args[2], PlayerOptions{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type stubWorkerInnerBackend struct {
	mu            sync.Mutex
	ops           []string
	source        Source
	cfg           config
	status        Status
	overlay       bool
	frame         image.Image
	lastFrameSize [2]int
}

func (b *stubWorkerInnerBackend) record(op string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ops = append(b.ops, op)
}

func (b *stubWorkerInnerBackend) hasOp(op string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Contains(b.ops, op)
}

func (b *stubWorkerInnerBackend) Load(s Source) error {
	b.mu.Lock()
	b.source = s
	b.status = Status{
		State:       Paused,
		CurrentTime: 0,
		Duration:    10 * time.Second,
		CaptionText: "initial line",
		CaptionTracks: []Track{
			{ID: "1", Label: "Embedded", Language: "en", Kind: "subtitles", Selected: false},
			{ID: "2", Label: "Sidecar", Language: "en", Kind: "subtitles", Selected: true},
		},
	}
	b.status.captionFiles = map[string]string{"1": ""}
	if len(s.Captions) > 0 {
		b.status.captionFiles["2"] = s.Captions[0].Source
	}
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	b.frame = img
	b.mu.Unlock()
	b.record("load")
	return nil
}

func (b *stubWorkerInnerBackend) Play() error {
	b.mu.Lock()
	b.status.State = Playing
	b.mu.Unlock()
	b.record("play")
	return nil
}

func (b *stubWorkerInnerBackend) Pause() error {
	b.mu.Lock()
	b.status.State = Paused
	b.mu.Unlock()
	b.record("pause")
	return nil
}

func (b *stubWorkerInnerBackend) Seek(t time.Duration) error {
	b.mu.Lock()
	b.status.CurrentTime = t
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 99, G: 88, B: 77, A: 255})
	b.frame = img
	b.mu.Unlock()
	b.record("seek")
	return nil
}

func (b *stubWorkerInnerBackend) SetVolume(float64) error { return nil }
func (b *stubWorkerInnerBackend) SetMuted(bool) error     { return nil }

func (b *stubWorkerInnerBackend) SelectTrack(kind, id string) error {
	b.mu.Lock()
	for i := range b.status.CaptionTracks {
		b.status.CaptionTracks[i].Selected = b.status.CaptionTracks[i].ID == id
	}
	b.mu.Unlock()
	b.record("track:" + kind + ":" + id)
	return nil
}

func (b *stubWorkerInnerBackend) setCaptionOverlay(enabled bool) error {
	b.mu.Lock()
	b.overlay = enabled
	b.mu.Unlock()
	if enabled {
		b.record("overlay:on")
	} else {
		b.record("overlay:off")
	}
	return nil
}

func (b *stubWorkerInnerBackend) Layout(CellRect, bool) error { return nil }
func (b *stubWorkerInnerBackend) SetFit(FitMode) error        { return nil }

func (b *stubWorkerInnerBackend) Poll() (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.status
	s.CaptionTracks = slices.Clone(b.status.CaptionTracks)
	return s, nil
}

func (b *stubWorkerInnerBackend) Frame(w, h int) (image.Image, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastFrameSize = [2]int{w, h}
	img := b.frame
	b.frame = nil
	return img, nil
}

func (b *stubWorkerInnerBackend) Close() error {
	b.record("close")
	return nil
}

func TestWorkerDecoderRefusesNonBareNames(t *testing.T) {
	d := NewWorkerDecoder(t.TempDir(), PlayerOptions{})
	defer d.Close()
	for _, bad := range []string{"", ".", "..", "../clip.mp4", "/etc/passwd", "a/b", `a\b`, "-flag", "a:b", "a\x00b", "a\nb", strings.Repeat("x", 256), string([]byte{255})} {
		if err := d.Load(ntmediabox.OpenRequest{Name: bad}); err == nil || !strings.Contains(err.Error(), "bad media name") {
			t.Errorf("Load(Name=%q) = %v, want bad media name", bad, err)
		}
		if err := d.Load(ntmediabox.OpenRequest{
			Name:     "video.mp4",
			Captions: []ntmediabox.Caption{{Name: bad}},
		}); err == nil || !strings.Contains(err.Error(), "bad caption name") {
			t.Errorf("Load(Caption=%q) = %v, want bad caption name", bad, err)
		}
	}
}

func TestWorkerBackendRoundTripAndCues(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native worker backend is not used on js/wasm")
	}
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "sample.mp4")
	if err := os.WriteFile(videoPath, []byte("fake-mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	srtPath := filepath.Join(dir, "sample.srt")
	srtData := "1\n00:00:01,000 --> 00:00:04,000\nhello from sidecar\n"
	if err := os.WriteFile(srtPath, []byte(srtData), 0o600); err != nil {
		t.Fatal(err)
	}

	inner := &stubWorkerInnerBackend{}
	var stagedDir string
	cfg := config{
		volume:               1,
		muted:                true,
		captionEvents:        true,
		nativeCaptionOverlay: true,
		useWorker:            true,
		workerStart: func(_ config, mediaDir, _ string) (io.ReadWriteCloser, func() error, error) {
			stagedDir = mediaDir
			a, b := net.Pipe()
			dec := &workerDecoder{
				dir:  mediaDir,
				opts: PlayerOptions{DisableHardwareDecoding: true, DisableAudio: true},
				create: func(c config) (backend, error) {
					inner.mu.Lock()
					inner.cfg = c
					inner.mu.Unlock()
					return inner, nil
				},
			}
			go ntmediabox.Serve(b, func(kind string) (ntmediabox.Decoder, error) {
				return dec, nil
			})
			return a, func() error {
				_ = a.Close()
				return b.Close()
			}, nil
		},
	}

	wb, err := createBackend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	err = wb.Load(Source{
		URL: videoPath,
		Captions: []Caption{
			{Source: srtPath, Label: "Sidecar", Language: "en", Default: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify staging directory has symlinked/linked video.mp4 and copied sub-0.srt.
	if _, err := os.Stat(filepath.Join(stagedDir, "video.mp4")); err != nil {
		t.Fatalf("staged video missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stagedDir, "sub-0.srt")); err != nil {
		t.Fatalf("staged sidecar missing: %v", err)
	}

	// Poll until initial paused status and frame arrive.
	deadline := time.Now().Add(2 * time.Second)
	var gotStatus Status
	var gotFrame image.Image
	for time.Now().Before(deadline) {
		s, err := wb.Poll()
		if err != nil {
			t.Fatal(err)
		}
		f, err := wb.Frame(320, 180)
		if err != nil {
			t.Fatal(err)
		}
		if f != nil {
			gotFrame = f
		}
		if s.State == Paused && s.Duration == 10*time.Second && gotFrame != nil {
			gotStatus = s
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if gotFrame == nil || gotStatus.State != Paused {
		t.Fatalf("did not receive initial paused frame and status: frame=%v status=%+v", gotFrame, gotStatus)
	}
	if !gotStatus.CaptionCuesAvailable || gotStatus.cueData == nil || gotStatus.cueData.timeline == nil {
		t.Fatalf("expected sidecar cue timeline to be attached: %+v", gotStatus)
	}
	if gotStatus.CaptionText != "initial line" {
		t.Fatalf("CaptionText = %q, want %q", gotStatus.CaptionText, "initial line")
	}

	// Exercise Play, Seek, SelectTrack, and setCaptionOverlay.
	if err := wb.Play(); err != nil {
		t.Fatal(err)
	}
	if err := wb.Seek(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := wb.SelectTrack("captions", "1"); err != nil {
		t.Fatal(err)
	}
	if ov, ok := wb.(interface{ setCaptionOverlay(bool) error }); ok {
		if err := ov.setCaptionOverlay(false); err != nil {
			t.Fatal(err)
		}
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, err := wb.Poll()
		if err != nil {
			t.Fatal(err)
		}
		if s.CurrentTime == 2*time.Second && len(s.CaptionTracks) == 2 && s.CaptionTracks[0].Selected {
			// Track 1 is embedded (not a sidecar), so CaptionCuesAvailable must be false.
			if s.CaptionCuesAvailable {
				t.Fatalf("embedded track should not report sidecar cue timeline: %+v", s)
			}
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	if err := wb.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stagedDir); !os.IsNotExist(err) {
		t.Fatalf("expected staging directory %s to be removed on Close, stat err = %v", stagedDir, err)
	}
}

func TestWorkerBackendHelperDeathReportsFailed(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native worker backend is not used on js/wasm")
	}
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "sample.mp4")
	if err := os.WriteFile(videoPath, []byte("fake-mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	var remoteEnd net.Conn
	closedCount := make(chan struct{}, 1)
	cfg := config{
		useWorker: true,
		workerStart: func(_ config, mediaDir, _ string) (io.ReadWriteCloser, func() error, error) {
			a, b := net.Pipe()
			remoteEnd = b
			inner := &stubWorkerInnerBackend{}
			dec := &workerDecoder{
				dir:    mediaDir,
				create: func(config) (backend, error) { return inner, nil },
			}
			go ntmediabox.Serve(b, func(string) (ntmediabox.Decoder, error) { return dec, nil })
			return a, func() error {
				select {
				case closedCount <- struct{}{}:
				default:
				}
				return a.Close()
			}, nil
		},
	}

	wb, err := createBackend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()
	if err := wb.Load(Source{URL: videoPath}); err != nil {
		t.Fatal(err)
	}
	_ = remoteEnd.Close() // simulate sudden helper death

	select {
	case <-closedCount:
	case <-time.After(2 * time.Second):
		t.Fatal("expected closeFn to be called automatically when helper dies")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, err := wb.Poll()
		if s.State == Failed && err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected Poll to report Failed after worker death")
}

func TestWorkerBackendReloadResetsGenerationAndStatus(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native worker backend is not used on js/wasm")
	}
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "sample.mp4")
	if err := os.WriteFile(videoPath, []byte("fake-mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	var pollDuringHandshake State
	var wb backend
	callNum := 0
	cfg := config{
		useWorker: true,
		workerStart: func(_ config, mediaDir, _ string) (io.ReadWriteCloser, func() error, error) {
			callNum++
			a, b := net.Pipe()
			n := callNum
			inner := &stubWorkerInnerBackend{}
			dec := &workerDecoder{
				dir:    mediaDir,
				create: func(config) (backend, error) { return inner, nil },
			}
			go ntmediabox.Serve(b, func(string) (ntmediabox.Decoder, error) {
				if n == 2 {
					st, _ := wb.Poll()
					pollDuringHandshake = st.State
				}
				return dec, nil
			})
			return a, func() error { return a.Close() }, nil
		},
	}

	var err error
	wb, err = createBackend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	if err := wb.Load(Source{URL: videoPath}); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if st, _ := wb.Poll(); st.State == Paused {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for range 5 {
		_ = wb.Seek(time.Second)
	}
	if err := wb.Load(Source{URL: videoPath}); err != nil {
		t.Fatal(err)
	}
	if pollDuringHandshake != Loading {
		t.Fatalf("Poll() during second Load handshake returned %v, want Loading", pollDuringHandshake)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s, err := wb.Poll()
		if err != nil {
			t.Fatal(err)
		}
		if s.State == Paused && s.Duration == 10*time.Second {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := wb.Poll()
	t.Fatalf("second Load dropped Gen=1 status; Poll() = %+v", s)
}

func testSandboxStage(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		return ""
	}
	stageBin := filepath.Join(t.TempDir(), "ntconfine-stage")
	cmd := exec.Command("go", "build", "-o", stageBin, "nimbleterminal.dev/ntconfine/cmd/ntconfine-stage")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ntconfine-stage: %v: %s", err, out)
	}
	return stageBin
}

func TestMPVWorkerPlayback(t *testing.T) { testMPVWorkerPlayback(t, "") }

func testMPVWorkerPlayback(t *testing.T, libraryPath string) {
	if runtime.GOOS == "js" {
		t.Skip("native worker backend is not used on js/wasm")
	}
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}

	stagePath := testSandboxStage(t)

	srtPath := filepath.Join(t.TempDir(), "sub.srt")
	if err := os.WriteFile(srtPath, []byte("1\n00:00:00,000 --> 00:00:03,000\nsandboxed subtitle\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wcfg := WorkerConfig{
		WorkerPath:       exe,
		WorkerArgs:       []string{testWorkerArg},
		StagePath:        stagePath,
		AllowUnsandboxed: runtime.GOOS == "windows",
	}
	cfg := config{
		libraryPath:          libraryPath,
		volume:               1,
		muted:                true,
		captionEvents:        true,
		nativeCaptionOverlay: true,
		useWorker:            true,
		worker:               wcfg,
	}
	wb, err := createBackend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()

	if err := wb.Load(Source{
		URL:      source,
		Captions: []Caption{{Source: srtPath, Label: "English", Language: "en", Default: true}},
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var gotFrame image.Image
	var gotStatus Status
	for time.Now().Before(deadline) {
		s, err := wb.Poll()
		if err != nil {
			t.Fatalf("Poll failed: %v", err)
		}
		f, err := wb.Frame(320, 180)
		if err != nil {
			t.Fatalf("Frame failed: %v", err)
		}
		if f != nil {
			gotFrame = f
		}
		if s.State == Paused && s.Duration > 0 && gotFrame != nil && len(s.CaptionTracks) > 0 {
			gotStatus = s
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if gotFrame == nil || gotStatus.State != Paused {
		t.Fatalf("worker did not settle paused with initial frame: frame=%v status=%+v", gotFrame, gotStatus)
	}
	if !gotStatus.CaptionCuesAvailable || gotStatus.cueData == nil || gotStatus.cueData.timeline == nil || len(gotStatus.cueData.timeline.cues) != 1 || gotStatus.cueData.timeline.cues[0].Text != "sandboxed subtitle" {
		t.Fatalf("expected sidecar cue timeline to be available: %+v", gotStatus)
	}
}
