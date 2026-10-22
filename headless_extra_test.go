package ntvideo

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testTranscript = "1\n00:00:00,250 --> 00:00:01,500\n<b>Hello</b> &amp; world\nSecond line\n\n2\n00:00:01,600 --> 00:00:01,900\nGoodbye\n"

type countingCueReader struct{ n int }

func (r *countingCueReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.n += len(p)
	return len(p), nil
}

type failingCueReader struct{}

func (failingCueReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestParseCuesPublic(t *testing.T) {
	cues, err := ParseCues(strings.NewReader(testTranscript), CueSRT)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[0].Start != 250*time.Millisecond || cues[0].Text != "Hello & world\nSecond line" || cues[0].TrackID != "" {
		t.Fatal(cues)
	}
	vtt := "WEBVTT\n\n" + strings.ReplaceAll(testTranscript, ",", ".")
	other, err := ParseCues(strings.NewReader(vtt), CueWebVTT)
	if err != nil || !reflect.DeepEqual(cues, other) {
		t.Fatal(other, err)
	}
	if _, err := ParseCues(strings.NewReader(testTranscript), CueWebVTT); err == nil {
		t.Fatal("missing WebVTT header accepted")
	}
	if _, err := ParseCues(strings.NewReader(vtt), CueSRT); err == nil {
		t.Fatal("wrong format accepted")
	}
	if _, err := ParseCues(nil, CueSRT); err == nil {
		t.Fatal("nil reader accepted")
	}
	if _, err := ParseCues(failingCueReader{}, CueSRT); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	r := &countingCueReader{}
	if _, err := ParseCues(r, CueSRT); err == nil || r.n != maxCueBytes+1 {
		t.Fatal("unbounded parser", r.n, err)
	}
	r = &countingCueReader{}
	if _, err := ParseCues(r, "ass"); err == nil || r.n != 0 {
		t.Fatal("unsupported format was read")
	}
}
func TestHeadlessCueSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captions.srt")
	if err := os.WriteFile(path, []byte(testTranscript), 0600); err != nil {
		t.Fatal(err)
	}
	opts := HeadlessOptions{FFprobe: "missing", FFmpeg: "missing"}
	cues, err := Cues(context.Background(), path, opts)
	if err != nil || len(cues) != 2 || cues[0].TrackID != "0" {
		t.Fatal(cues, err)
	}
	opts.SubtitleStream = 1
	if _, err := Cues(context.Background(), path, opts); err == nil {
		t.Fatal("invalid stream accepted")
	}
	opts.SubtitleStream = -1
	if _, err := Cues(context.Background(), path, opts); err == nil {
		t.Fatal("negative stream accepted")
	}
	if _, err := Cues(context.Background(), path, HeadlessOptions{Limits: Limits{MaxFileBytes: 4}}); err == nil {
		t.Fatal("file limit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Cues(ctx, path, HeadlessOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Cues(context.Background(), "https://example.com/captions.srt", HeadlessOptions{}); err == nil {
		t.Fatal("URL accepted")
	}
}
func TestFramesRequestLimits(t *testing.T) {
	frames, err := Frames(context.Background(), "missing", nil, HeadlessOptions{FFprobe: "missing", FFmpeg: "missing"})
	if err != nil || len(frames) != 0 {
		t.Fatal(frames, err)
	}
	if _, err := Frames(context.Background(), "missing", make([]time.Duration, 17), HeadlessOptions{}); err == nil {
		t.Fatal("batch limit ignored")
	}
	if _, err := Frames(context.Background(), "missing", []time.Duration{0}, HeadlessOptions{MaxFrameWidth: -1}); err == nil {
		t.Fatal("negative dimensions accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Frames(ctx, "missing", []time.Duration{0}, HeadlessOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func samePixels(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false
			}
		}
	}
	return true
}
func TestHeadlessMediaBatchFrames(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	opts := HeadlessOptions{MaxFrameWidth: 80, MaxFrameHeight: 45}
	frames, err := Frames(context.Background(), source, []time.Duration{time.Second, 0, time.Second}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || frames[0].Bounds().Dx() > 80 || frames[0].Bounds().Dy() > 45 {
		t.Fatal(frames)
	}
	for i, at := range []time.Duration{time.Second, 0, time.Second} {
		single, err := FrameAt(context.Background(), source, at, opts)
		if err != nil || !samePixels(frames[i], single) {
			t.Fatal("wrong frame order/content", i, err)
		}
	}
	if frames[0] == frames[2] {
		t.Fatal("duplicate frames share mutable pixels")
	}
	info, err := Probe(context.Background(), source, HeadlessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if frames, err := Frames(context.Background(), source, []time.Duration{0, info.Duration}, opts); err == nil || frames != nil {
		t.Fatal("EOF returned partial frames")
	}
	if _, err := Frames(context.Background(), source, make([]time.Duration, 16), HeadlessOptions{MaxFrameWidth: 4096, MaxFrameHeight: 2160}); err == nil {
		t.Fatal("aggregate pixel limit ignored")
	}
	if runtime.GOOS == "windows" {
		return
	} // Counting wrapper below uses a POSIX shell.
	tool, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	wrapper := filepath.Join(dir, "ffmpeg")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nprintf 'call\\n' >> " + quote(log) + "\nexec " + quote(tool) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	times := make([]time.Duration, 9)
	for i := range times {
		times[i] = (info.Duration - 100*time.Millisecond) * time.Duration(i) / 9
	}
	start := time.Now()
	frames, err = Frames(context.Background(), source, times, HeadlessOptions{FFmpeg: wrapper})
	if err != nil || len(frames) != 9 {
		t.Fatal(len(frames), err)
	}
	data, err := os.ReadFile(log)
	if err != nil || bytes.Count(data, []byte("call\n")) != 1 {
		t.Fatal("expected one FFmpeg process", string(data), err)
	}
	t.Logf("nine sampled frames in %s using one FFmpeg process", time.Since(start))
}

func TestHeadlessMediaContainerCues(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "en.srt")
	second := filepath.Join(dir, "es.srt")
	for path, text := range map[string]string{first: testTranscript, second: strings.ReplaceAll(testTranscript, "Goodbye", "Adios")} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "subtitled.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := runMediaTool(ctx, "ffmpeg", 4096, "-v", "error", "-nostdin", "-i", source, "-i", first, "-i", second, "-t", "2", "-map", "0:v:0", "-map", "0:a:0?", "-map", "1:0", "-map", "2:0", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", "-c:s", "mov_text", "-metadata:s:s:0", "language=eng", "-metadata:s:s:0", "handler_name=English captions", "-metadata:s:s:1", "language=spa", "-metadata:s:s:1", "handler_name=Spanish captions", "-disposition:s:0", "default", "-disposition:s:1", "forced", path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Probe(ctx, path, HeadlessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Subtitles) != 2 || info.Subtitles[0].Language != "eng" || info.Subtitles[1].Title != "Spanish captions" || !info.Subtitles[0].Default || !info.Subtitles[1].Forced || info.Subtitles[1].Index != 1 || info.Subtitles[0].StreamIndex == info.Subtitles[1].StreamIndex {
		t.Fatal(info.Subtitles)
	}
	cues, err := Cues(ctx, path, HeadlessOptions{SubtitleStream: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[1].Text != "Adios" || cues[0].Start != 250*time.Millisecond || cues[0].TrackID != "1" {
		t.Fatal(cues)
	}
	if _, err := Cues(ctx, path, HeadlessOptions{SubtitleStream: 2}); err == nil {
		t.Fatal("invalid ordinal accepted")
	}
	if _, err := Cues(ctx, source, HeadlessOptions{}); !errors.Is(err, ErrNoSubtitles) {
		t.Fatal("expected no subtitle streams", err)
	}
}
