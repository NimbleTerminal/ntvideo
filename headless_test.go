package ntvideo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalFileLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"https://example.com/x", "edl://x", "fd://0", "file:///tmp/x", "concat:x", `\\server\share\clip`, "//server/share", path + "\x00"} {
		if _, err := validateLocalFile(s, Limits{}); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	if _, err := validateLocalFile(path, Limits{MaxFileBytes: 3}); err == nil {
		t.Fatal("size limit ignored")
	}
	if _, err := validateLocalFile(filepath.Dir(path), Limits{}); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := validateLocalFile(path, Limits{}); err != nil {
		t.Fatal(err)
	}
	if err := checkDimensions(5000, 100, Limits{}); err == nil {
		t.Fatal("width limit ignored")
	}
	if err := checkDimensions(4096, 4096, Limits{}); err == nil {
		t.Fatal("pixel limit ignored")
	}
}
func TestHeadlessToolsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clip.mp4")
	os.WriteFile(path, []byte("test"), 0600)
	_, err := Probe(context.Background(), path, HeadlessOptions{FFprobe: filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		t.Fatal("missing tool accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Probe(ctx, path, HeadlessOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestHeadlessMedia(t *testing.T) {
	path := os.Getenv("NTVIDEO_TEST_FILE")
	if path == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	info, err := Probe(context.Background(), path, HeadlessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 || info.VideoCodec == "" || !info.HasAudio || info.AudioCodec == "" {
		t.Fatal(info)
	}
	img, err := FrameAt(context.Background(), path, time.Second, HeadlessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != info.Width || img.Bounds().Dy() != info.Height {
		t.Fatal(img.Bounds())
	}
	if _, err = FrameAt(context.Background(), path, info.Duration, HeadlessOptions{}); err == nil {
		t.Fatal("EOF accepted")
	}
	if _, err = Probe(context.Background(), path, HeadlessOptions{Limits: Limits{MaxWidth: 100}}); err == nil {
		t.Fatal("resolution limit ignored")
	}
}

func TestHeadlessMediaRejectsNetworkReferences(t *testing.T) {
	if os.Getenv("NTVIDEO_TEST_FILE") == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "blocked", 404) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "network.m3u8")
	playlist := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\n" + server.URL + "/segment.ts\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(path, []byte(playlist), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), path, HeadlessOptions{}); err == nil {
		t.Fatal("network playlist accepted")
	}
	if _, err := Cues(context.Background(), path, HeadlessOptions{}); err == nil {
		t.Fatal("transcript extraction accepted network playlist")
	}
	if _, err := Frames(context.Background(), path, []time.Duration{0}, HeadlessOptions{}); err == nil {
		t.Fatal("batch frames accepted network playlist")
	}
	if requests.Load() != 0 {
		t.Fatal("ffprobe accessed network")
	}
}
