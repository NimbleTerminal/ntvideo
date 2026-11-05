package ntvideo

import (
	"context"
	"encoding/json"
	"errors"
	"image"
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

func TestWorkerCueMetadataValidation(t *testing.T) {
	valid := []CaptionCue{{ID: "0", Start: time.Second, End: 2 * time.Second, Text: "hello"}, {ID: "1", Start: time.Second, End: 1500 * time.Millisecond, Text: "overlap"}}
	encode := func(cues []CaptionCue) []byte {
		data, _ := json.Marshal(workerCueMetadata{Version: 1, Cues: &cues})
		return data
	}
	timeline, err := decodeWorkerCues(encode(valid))
	if err != nil || len(timeline.cues) != 2 {
		t.Fatalf("timeline=%v err=%v", timeline, err)
	}
	bad := [][]byte{nil, []byte(`null`), []byte(`{}`), []byte(`{"version":2,"cues":[]}`), []byte(`{"version":1,"cues":null}`), []byte(strings.Repeat(" ", maxWorkerCueMetadata+1))}
	for _, mutate := range []func(*CaptionCue){
		func(c *CaptionCue) { c.ID = "duplicate" }, func(c *CaptionCue) { c.TrackID = "1" },
		func(c *CaptionCue) { c.Start = -1 }, func(c *CaptionCue) { c.End = c.Start },
		func(c *CaptionCue) { c.Text = "\x1b[31munsafe" },
		func(c *CaptionCue) { c.Text = "safe\u202eunsafe" },
		func(c *CaptionCue) { c.Text = "safe\u2066unsafe" },
		func(c *CaptionCue) { c.Text = "safe\u200funsafe" }, func(c *CaptionCue) { c.Text = strings.Repeat("x", 4097) },
	} {
		cue := valid[0]
		mutate(&cue)
		bad = append(bad, encode([]CaptionCue{cue}))
	}
	bad = append(bad, encode(make([]CaptionCue, maxCaptionCues+1)))
	for i, data := range bad {
		if _, err := decodeWorkerCues(data); err == nil {
			t.Errorf("accepted invalid metadata %d", i)
		}
	}
}

func TestWorkerCueHandlerBoundsAndNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:01,000 --> 00:00:02,000\nhello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := workerCueHandler(dir)
	resp, err := handler(ntmediabox.RenderRequest{Kind: "video-caption-cues", Name: "sub.srt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWorkerCues(resp.Meta); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../sub.srt", "/sub.srt", "sub.mp4"} {
		if _, err := handler(ntmediabox.RenderRequest{Kind: "video-caption-cues", Name: name}); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxWorkerSidecarBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := handler(ntmediabox.RenderRequest{Kind: "video-caption-cues", Name: "sub.srt"}); err == nil {
		t.Fatal("accepted oversized sidecar")
	}
}

func TestWorkerCueErrorsDoNotParseHostSidecar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sub.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nvalid in host\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []bool{false, true} {
		closed := false
		wb := &workerBackend{cfg: config{}, starter: func(config, string, string) (io.ReadWriteCloser, func() error, error) {
			a, b := net.Pipe()
			go ntmediabox.ServeRender(b, ntmediabox.RenderFunc(func(ntmediabox.RenderRequest) (ntmediabox.RenderResponse, error) {
				if malformed {
					return ntmediabox.RenderResponse{Meta: []byte(`{"version":999}`)}, nil
				}
				return ntmediabox.RenderResponse{}, errors.New("worker parse failed")
			}))
			return a, func() error { closed = true; _ = b.Close(); return a.Close() }, nil
		}}
		files := wb.loadWorkerCues(context.Background(), dir, "unused", []ntmediabox.Caption{{Name: "sub.srt"}})
		if len(files) != 1 || files[0].timeline != nil || files[0].err == nil || !closed {
			t.Fatalf("unexpected fallback or cleanup: %+v closed=%v", files, closed)
		}
	}
}

func TestWorkerDecoderLimitsCannotBeWidened(t *testing.T) {
	var got Limits
	d := &workerDecoder{dir: t.TempDir(), opts: PlayerOptions{Limits: Limits{MaxFileBytes: 100, MaxWidth: 20, MaxHeight: 10, MaxPixels: 200}}, create: func(c config) (backend, error) { got = c.limits; return &stubWorkerInnerBackend{}, nil }}
	defer d.Close()
	if err := d.Load(ntmediabox.OpenRequest{Name: "video.mp4", Limits: ntmediabox.Limits{MaxFileBytes: 1000, MaxWidth: 100, MaxHeight: 100, MaxPixels: 10000}}); err != nil {
		t.Fatal(err)
	}
	if got != d.opts.Limits {
		t.Fatalf("widened worker limits: %+v", got)
	}
	if err := d.Load(ntmediabox.OpenRequest{Name: "video.mp4", Limits: ntmediabox.Limits{MaxWidth: 5}}); err != nil {
		t.Fatal(err)
	}
	if got.MaxWidth != 5 || got.MaxHeight != 10 {
		t.Fatalf("limits=%+v", got)
	}
}

func TestWorkerBackendRejectsLateSessionAndCommandMetadata(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	client := ntmediabox.NewClient(a, nil, nil)
	defer client.Close()
	wb := &workerBackend{client: client, session: 2, reqGen: 2, visible: true, width: 4, height: 2}
	wb.onStatus(1, ntmediabox.Status{Gen: 2, Duration: 99 * time.Second})
	wb.onFrame(1, ntmediabox.Frame{Gen: 2, Image: image.NewRGBA(image.Rect(0, 0, 4, 2))})
	wb.onStatus(2, ntmediabox.Status{Gen: 1, Duration: 99 * time.Second})
	wb.onFrame(2, ntmediabox.Frame{Gen: 1, Image: image.NewRGBA(image.Rect(0, 0, 4, 2))})
	if wb.hasStatus || wb.latestFrame != nil {
		t.Fatal("accepted obsolete worker metadata/frame")
	}
	wb.onStatus(2, ntmediabox.Status{Gen: 2, Duration: time.Second})
	wb.onFrame(2, ntmediabox.Frame{Gen: 2, Image: image.NewRGBA(image.Rect(0, 0, 5, 2))})
	if !wb.hasStatus || wb.latestFrame != nil {
		t.Fatal("status rejected or oversized stale frame accepted")
	}
	wb.onFrame(2, ntmediabox.Frame{Gen: 2, Image: image.NewRGBA(image.Rect(0, 0, 4, 2))})
	if wb.latestFrame == nil {
		t.Fatal("current frame rejected")
	}
}

func TestWorkerMetadataTimeoutClosesHelper(t *testing.T) {
	closed := 0
	wb := &workerBackend{starter: func(config, string, string) (io.ReadWriteCloser, func() error, error) {
		a, b := net.Pipe()
		return a, func() error { closed++; return b.Close() }, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	files := wb.loadWorkerCues(ctx, "unused", "unused", []ntmediabox.Caption{{Name: "sub.srt"}})
	if !errors.Is(files[0].err, context.DeadlineExceeded) || closed != 1 {
		t.Fatalf("timeout=%v helper closes=%d", files[0].err, closed)
	}
}

func TestWorkerCaptionMetadataErrorKeepsPlayback(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native worker backend")
	}
	dir := t.TempDir()
	video, sub := filepath.Join(dir, "video.mp4"), filepath.Join(dir, "sub.srt")
	for _, path := range []string{video, sub} {
		if err := os.WriteFile(path, []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	wb := &workerBackend{width: 4, height: 2, visible: true, cfg: config{captionEvents: true}}
	wb.starter = func(_ config, dir, _ string) (io.ReadWriteCloser, func() error, error) {
		calls++
		a, b := net.Pipe()
		if calls == 1 {
			go ntmediabox.ServeRender(b, ntmediabox.RenderFunc(func(ntmediabox.RenderRequest) (ntmediabox.RenderResponse, error) {
				return ntmediabox.RenderResponse{}, errors.New("malformed captions")
			}))
		} else {
			go serveWorker(b, dir, func(string) (ntmediabox.Decoder, error) {
				return &workerDecoder{dir: dir, create: func(config) (backend, error) { return &stubWorkerInnerBackend{}, nil }}, nil
			})
		}
		return a, func() error { _ = b.Close(); return a.Close() }, nil
	}
	defer wb.Close()
	if err := wb.Load(Source{URL: video, Captions: []Caption{{Source: sub}}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := wb.Poll()
		if err != nil {
			t.Fatal(err)
		}
		if status.State == Paused {
			if status.CaptionCueErr == nil || status.CaptionCuesAvailable {
				t.Fatalf("caption error missing: %+v", status)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("caption metadata error prevented playback")
}
