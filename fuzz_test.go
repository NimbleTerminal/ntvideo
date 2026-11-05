package ntvideo

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func hasDisallowedCaptionRune(s string) bool {
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if r == '\u061c' || r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			return true
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func FuzzParseCues(f *testing.F) {
	f.Add([]byte("1\n00:00:00,000 --> 00:00:02,500\nHello <b>world</b> &amp; friends\n\n2\n00:00:02,500 --> 00:00:04,000\nSecond line\n"))
	f.Add([]byte("\ufeffWEBVTT\n\nNOTE comment\n\n00:00.000 --> 00:01.000 align:start\n<c.yellow>Hello</c>\n"))
	f.Add([]byte("WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n\n00:00.000 --> 00:01.000\nx\n"))
	f.Add([]byte("1\n00:00:01,000 --> 00:00:00,500\nbackwards\n"))
	f.Add([]byte("1\n00:00:00,000 --> 00:00:01,000\n\x1b[31mred\u202ebidi\x00ctrl\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, format := range []CueFormat{CueSRT, CueWebVTT} {
			cues, err := ParseCues(bytes.NewReader(data), format)
			if err != nil {
				continue
			}
			if len(cues) > maxCaptionCues {
				t.Fatalf("cue count %d exceeds %d", len(cues), maxCaptionCues)
			}
			for i, cue := range cues {
				if cue.ID != strconv.Itoa(i) {
					t.Fatalf("cue[%d].ID = %q, want %d", i, cue.ID, i)
				}
				if cue.TrackID != "" {
					t.Fatalf("cue[%d].TrackID = %q, want empty", i, cue.TrackID)
				}
				if cue.Start < 0 || cue.End <= cue.Start {
					t.Fatalf("cue[%d] invalid timing: %v -> %v", i, cue.Start, cue.End)
				}
				if !utf8.ValidString(cue.Text) {
					t.Fatalf("cue[%d].Text is not valid UTF-8", i)
				}
				if len([]rune(cue.Text)) > 4096 {
					t.Fatalf("cue[%d].Text rune length exceeds 4096", i)
				}
				if cue.Text != captionPlainText(cue.Text) || hasDisallowedCaptionRune(cue.Text) {
					t.Fatalf("cue[%d].Text was not sanitized: %q", i, cue.Text)
				}
			}
			if len(data) <= maxCueBytes {
				nonNil := cues
				if nonNil == nil {
					nonNil = []CaptionCue{}
				}
				encoded, err := json.Marshal(workerCueMetadata{Version: 1, Cues: &nonNil})
				if err != nil {
					t.Fatalf("marshal workerCueMetadata: %v", err)
				}
				timeline, err := decodeWorkerCues(encoded)
				if err != nil {
					t.Fatalf("decodeWorkerCues rejected ParseCues output: %v", err)
				}
				if len(timeline.cues) != len(cues) {
					t.Fatalf("decoded %d cues, want %d", len(timeline.cues), len(cues))
				}
			}
		}
	})
}

func FuzzDecodeWorkerCues(f *testing.F) {
	f.Add([]byte(`{"version":1,"cues":[]}`))
	f.Add([]byte(`{"version":1,"cues":[{"ID":"0","TrackID":"","Start":0,"End":1000000000,"Text":"hello"}]}`))
	f.Add([]byte(`{"version":2,"cues":[]}`))
	f.Add([]byte(`{"version":1}`))
	f.Add([]byte(`{"version":1,"cues":[{"ID":"0","TrackID":"1","Start":0,"End":1000,"Text":"bad track"}]}`))
	f.Add([]byte(`{"version":1,"cues":[{"ID":"0","TrackID":"","Start":0,"End":1000,"Text":"bad\u202ebidi"}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		timeline, err := decodeWorkerCues(data)
		if err != nil {
			return
		}
		if timeline == nil {
			t.Fatal("nil timeline with nil error")
		}
		if len(timeline.cues) > maxCaptionCues {
			t.Fatalf("cues length %d exceeds %d", len(timeline.cues), maxCaptionCues)
		}
		if len(timeline.boundaries) != 2*len(timeline.cues) {
			t.Fatalf("boundaries length %d != 2*%d", len(timeline.boundaries), len(timeline.cues))
		}
		total := 0
		for i, cue := range timeline.cues {
			total += len(cue.Text)
			if total > maxCueBytes {
				t.Fatalf("total cue bytes %d exceed %d", total, maxCueBytes)
			}
			if cue.ID != strconv.Itoa(i) || cue.TrackID != "" || cue.Start < 0 || cue.End <= cue.Start {
				t.Fatalf("invalid cue[%d]: %+v", i, cue)
			}
			if !utf8.ValidString(cue.Text) || cue.Text != captionPlainText(cue.Text) || hasDisallowedCaptionRune(cue.Text) {
				t.Fatalf("unsanitized cue[%d].Text: %q", i, cue.Text)
			}
		}
		var sched cueScheduler
		snap := &cueSnapshot{track: "1", timeline: timeline, seekSerial: 1}
		_ = sched.observe(snap, 0, 10*time.Second, CueLoad, false)
		if len(timeline.cues) > 0 {
			first := timeline.cues[0]
			_ = sched.observe(snap, first.Start, first.End+time.Second, "", false)
			_ = sched.observe(snap, first.End, first.End+time.Second, "", false)
		}
		_ = sched.clear(CueClose)
	})
}

func FuzzCaptionPlainTextAndOverlay(f *testing.F) {
	f.Add("Hello\nworld", "line1\nline2\nline3\nline4", 20, 4)
	f.Add("\x1b[31mANSI\x1b[0m \u202ebidi \x00ctrl", "row1\nrow2", 10, 2)
	f.Add("مرحبا 世界", "a\nb\nc", 6, 3)

	f.Fuzz(func(t *testing.T, text, video string, width, height int) {
		plain := captionPlainText(text)
		if plain != captionPlainText(plain) {
			t.Fatalf("captionPlainText is not idempotent: %q != %q", plain, captionPlainText(plain))
		}
		if len([]rune(plain)) > 4096 {
			t.Fatalf("captionPlainText rune length %d > 4096", len([]rune(plain)))
		}
		if ansi.Strip(plain) != plain {
			t.Fatalf("ANSI sequence survived captionPlainText: %q", plain)
		}
		if hasDisallowedCaptionRune(plain) {
			t.Fatalf("disallowed control or bidi rune survived captionPlainText: %q", plain)
		}
		w := min(max(width, -2), 120)
		h := min(max(height, -2), 40)
		_ = overlayCaptions(video, text, w, h)
	})
}

func FuzzWorkerFilenamesAndSettings(f *testing.F) {
	f.Add("video.mp4", ".mp4", []byte(`{"version":1,"library_path":"/usr/lib/libmpv.so.2"}`))
	f.Add("../escape.mp4", ".a/b", []byte(`{"version":2,"library_path":"/usr/lib/libmpv.so.2"}`))
	f.Add("-flag.srt", ".SRT", []byte(`{"version":1,"library_path":"relative/libmpv.so"}`))
	f.Add("a:b", "..", []byte(`not-json`))
	f.Add("video.", ".", []byte(`{}`))

	f.Fuzz(func(t *testing.T, name, ext string, settings []byte) {
		if isBareFilename(name) {
			if name == "" || len(name) > 255 || !utf8.ValidString(name) {
				t.Fatalf("isBareFilename accepted invalid string %q", name)
			}
			if name == "." || name == ".." || strings.HasPrefix(name, "-") || strings.ContainsAny(name, `/\:`) {
				t.Fatalf("isBareFilename accepted unsafe name %q", name)
			}
			if filepath.Base(name) != name {
				t.Fatalf("filepath.Base(%q) = %q", name, filepath.Base(name))
			}
			for _, r := range name {
				if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
					t.Fatalf("isBareFilename accepted control rune U+%04X in %q", r, name)
				}
			}
		}

		sanitized := sanitizeExt(ext)
		if sanitized != "" {
			if !strings.HasPrefix(sanitized, ".") || len(sanitized) < 2 || len(sanitized) > 16 {
				t.Fatalf("invalid sanitized extension %q from %q", sanitized, ext)
			}
			for _, c := range sanitized[1:] {
				if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
					t.Fatalf("unexpected char %q in sanitized extension %q", c, sanitized)
				}
			}
		}
		if !isBareFilename("video" + sanitized) {
			t.Fatalf("staged video name %q is not a bare filename", "video"+sanitized)
		}
		if !isBareFilename("sub-0" + sanitized) {
			t.Fatalf("staged subtitle name %q is not a bare filename", "sub-0"+sanitized)
		}

		got, err := parseWorkerSettings(bytes.NewReader(settings))
		if err == nil {
			if got == "" || !filepath.IsAbs(got) {
				t.Fatalf("parseWorkerSettings accepted non-absolute path %q", got)
			}
		}
	})
}
