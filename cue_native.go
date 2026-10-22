//go:build (darwin || linux || windows) && (amd64 || arm64)

package ntvideo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type nativeCueFile struct {
	timeline *cueTimeline
	err      error
}

func readCueFile(path string) nativeCueFile {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".srt" && ext != ".vtt" {
		return nativeCueFile{}
	}
	f, err := os.Open(path)
	if err != nil {
		return nativeCueFile{err: err}
	}
	defer f.Close()
	format := CueSRT
	if ext == ".vtt" {
		format = CueWebVTT
	}
	cues, err := ParseCues(f, format)
	if err != nil {
		return nativeCueFile{err: err}
	}
	return nativeCueFile{timeline: newCueTimeline(cues)}
}
func (b *mpvBackend) captionSnapshot(s *Status) {
	if !b.cfg.captionEvents || !b.loaded || b.seekPending {
		return
	}
	snapshot := &cueSnapshot{seekSerial: b.cueSeekSerial}
	s.cueData = snapshot
	// Query current selection directly: the display metadata cache may lag.
	for i, n := 0, min(128, int(b.number("track-list/count"))); i < n; i++ {
		p := fmt.Sprintf("track-list/%d/", i)
		if b.text(p+"type") != "sub" || !b.flag(p+"selected") {
			continue
		}
		snapshot.track = b.text(p + "id")
		if file, ok := b.cueFiles[b.text(p+"external-filename")]; ok {
			snapshot.timeline = file.timeline
			s.CaptionCueErr = file.err
			s.CaptionCuesAvailable = file.timeline != nil
		}
		return
	}
}
