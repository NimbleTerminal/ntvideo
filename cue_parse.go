package ntvideo

import (
	"fmt"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxCueBytes = 4 << 20
const maxCaptionCues = 10000

// CueFormat identifies a UTF-8 subtitle sidecar format.
type CueFormat string

const (
	CueSRT    CueFormat = "srt"
	CueWebVTT CueFormat = "webvtt"
)

// ParseCues reads at most 4 MiB plus one overflow byte and returns at most 10,000
// cues in file order. IDs are distinct stable ordinals within the file; TrackID
// is empty. Text is plain, terminal-safe, and capped at 4096 runes per cue.
// WebVTT timestamp maps are unsupported. Errors return no partial transcript.
// The caller owns r; cancellation of a blocking reader is the caller's concern.
func ParseCues(r io.Reader, format CueFormat) ([]CaptionCue, error) {
	if format != CueSRT && format != CueWebVTT {
		return nil, fmt.Errorf("ntvideo: unsupported cue format %q", format)
	}
	if r == nil {
		return nil, fmt.Errorf("ntvideo: nil cue reader")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxCueBytes+1))
	if err != nil {
		return nil, err
	}
	header := strings.SplitN(strings.TrimPrefix(string(data), "\ufeff"), "\n", 2)[0]
	header = strings.TrimSuffix(header, "\r")
	vtt := header == "WEBVTT" || strings.HasPrefix(header, "WEBVTT ") || strings.HasPrefix(header, "WEBVTT\t")
	if (format == CueWebVTT) != vtt {
		return nil, fmt.Errorf("ntvideo: cue data does not match %s", format)
	}
	return parseCueData(data)
}

var cueTimestamp = regexp.MustCompile(`^(?:(\d{1,4}):)?(\d{2}):(\d{2})[.,](\d{3})$`)
var cueMarkup = regexp.MustCompile(`<[^>]*>`)

func parseCueTime(value string) (time.Duration, error) {
	m := cueTimestamp.FindStringSubmatch(value)
	if m == nil {
		return 0, fmt.Errorf("invalid caption timestamp %q", value)
	}
	parts := make([]int, 4)
	for i := range parts {
		parts[i], _ = strconv.Atoi(m[i+1])
	}
	if parts[1] >= 60 || parts[2] >= 60 {
		return 0, fmt.Errorf("invalid caption timestamp %q", value)
	}
	return time.Duration(parts[0])*time.Hour + time.Duration(parts[1])*time.Minute + time.Duration(parts[2])*time.Second + time.Duration(parts[3])*time.Millisecond, nil
}
func cueText(value string) string {
	return captionPlainText(html.UnescapeString(cueMarkup.ReplaceAllString(value, "")))
}
func parseCaptionCues(data []byte) (*cueTimeline, error) {
	cues, err := parseCueData(data)
	if err != nil {
		return nil, err
	}
	return newCueTimeline(cues), nil
}
func parseCueData(data []byte) ([]CaptionCue, error) {
	if len(data) > maxCueBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("caption cues require UTF-8 and at most %d bytes", maxCueBytes)
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	vtt := len(lines) > 0 && (lines[0] == "WEBVTT" || strings.HasPrefix(lines[0], "WEBVTT ") || strings.HasPrefix(lines[0], "WEBVTT\t"))
	if vtt {
		i := 1
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			if strings.HasPrefix(lines[i], "X-TIMESTAMP-MAP") {
				return nil, fmt.Errorf("WebVTT timestamp maps are not supported")
			}
			i++
		}
		lines = lines[i:]
	}
	var cues []CaptionCue
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		start := i
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			i++
		}
		block := lines[start:i]
		if vtt && (block[0] == "NOTE" || strings.HasPrefix(block[0], "NOTE ") || strings.HasPrefix(block[0], "NOTE\t") || block[0] == "STYLE" || block[0] == "REGION") {
			continue
		}
		if !strings.Contains(block[0], "-->") {
			block = block[1:]
		}
		if len(block) < 2 {
			return nil, fmt.Errorf("caption block at line %d has no timing or text", start+1)
		}
		fields := strings.Fields(block[0])
		if len(fields) < 3 || fields[1] != "-->" {
			return nil, fmt.Errorf("invalid caption timing at line %d", start+1)
		}
		begin, err := parseCueTime(fields[0])
		if err != nil {
			return nil, err
		}
		end, err := parseCueTime(fields[2])
		if err != nil {
			return nil, err
		}
		if end <= begin {
			return nil, fmt.Errorf("caption end must follow start")
		}
		if len(cues) >= maxCaptionCues {
			return nil, fmt.Errorf("caption cue count exceeds %d", maxCaptionCues)
		}
		cues = append(cues, CaptionCue{ID: strconv.Itoa(len(cues)), Start: begin, End: end, Text: cueText(strings.Join(block[1:], "\n"))})
	}
	return cues, nil
}
