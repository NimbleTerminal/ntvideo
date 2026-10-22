//go:build js && wasm

package ntvideo

import (
	"fmt"
	"math"
	"strconv"
	"syscall/js"
	"time"
)

type domCueCache struct {
	track, list js.Value
	count       int
	timeline    *cueTimeline
}

func (b *domBackend) captionSnapshot(s *Status) {
	if !b.cfg.captionEvents {
		return
	}
	if seeking := b.video.Get("seeking"); seeking.Type() == js.TypeBoolean && seeking.Bool() {
		return
	}
	snapshot := &cueSnapshot{seekSerial: b.cueSeekSerial}
	s.cueData = snapshot
	tracks := b.video.Get("textTracks")
	if tracks.IsNull() || tracks.IsUndefined() {
		return
	}
	for i := 0; i < tracks.Length(); i++ {
		track := tracks.Index(i)
		if track.Get("mode").String() != "showing" {
			continue
		}
		snapshot.track = strconv.Itoa(i)
		list := track.Get("cues")
		if list.IsNull() || list.IsUndefined() {
			s.cueData = nil
			return
		}
		count := list.Length()
		if count > maxCaptionCues {
			s.CaptionCueErr = fmt.Errorf("caption cue count exceeds %d", maxCaptionCues)
			return
		}
		cache := b.cueCache
		if cache.timeline != nil && cache.track.Equal(track) && cache.list.Equal(list) && cache.count == count {
			snapshot.timeline = cache.timeline
			s.CaptionCuesAvailable = true
			return
		}
		cues := make([]CaptionCue, 0, count)
		bytes := 0
		for j := 0; j < count; j++ {
			cue := list.Index(j)
			start, end := cue.Get("startTime").Float(), cue.Get("endTime").Float()
			text := cue.Get("text")
			if text.Type() != js.TypeString {
				s.CaptionCueErr = fmt.Errorf("caption cue is not a text cue")
				return
			}
			if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > float64((10000*time.Hour).Seconds()) {
				s.CaptionCueErr = fmt.Errorf("invalid caption cue timing")
				return
			}
			value := text.String()
			bytes += len(value)
			if bytes > maxCueBytes {
				s.CaptionCueErr = fmt.Errorf("caption cue text exceeds byte limit")
				return
			}
			cues = append(cues, CaptionCue{ID: strconv.Itoa(j), Start: time.Duration(start * float64(time.Second)), End: time.Duration(end * float64(time.Second)), Text: cueText(value)})
		}
		snapshot.timeline = newCueTimeline(cues)
		b.cueCache = domCueCache{track, list, count, snapshot.timeline}
		s.CaptionCuesAvailable = true
		return
	}
}
