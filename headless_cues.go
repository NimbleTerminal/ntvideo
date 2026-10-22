package ntvideo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrNoSubtitles = errors.New("ntvideo: source has no subtitle streams")
var ErrSubtitleNotText = errors.New("ntvideo: bitmap subtitles cannot be extracted as text")

type cueContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cueContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Cues extracts a bounded transcript without playback or libmpv. SRT/WebVTT
// sidecars are parsed directly. Containers use ffprobe then ffmpeg to convert
// opts.SubtitleStream (0:s:N) to WebVTT. No OCR is attempted for bitmap tracks.
// TrackID is the subtitle ordinal; use Probe for language/title and stream index.
func Cues(ctx context.Context, path string, opts HeadlessOptions) ([]CaptionCue, error) {
	ctx, cancel := opts.deadline(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.SubtitleStream < 0 {
		return nil, fmt.Errorf("ntvideo: subtitle stream must be nonnegative")
	}
	path, err := validateLocalFile(path, opts.Limits)
	if err != nil {
		return nil, err
	}
	var cues []CaptionCue
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".srt" || ext == ".vtt" {
		if opts.SubtitleStream != 0 {
			return nil, fmt.Errorf("ntvideo: sidecars have only subtitle stream 0")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		format := CueSRT
		if ext == ".vtt" {
			format = CueWebVTT
		}
		cues, err = ParseCues(cueContextReader{ctx, f}, format)
		if err != nil {
			return nil, err
		}
	} else {
		info, err := Probe(ctx, path, opts)
		if err != nil {
			return nil, err
		}
		if len(info.Subtitles) == 0 {
			return nil, ErrNoSubtitles
		}
		if opts.SubtitleStream >= len(info.Subtitles) {
			return nil, fmt.Errorf("ntvideo: subtitle stream %d out of range (have %d)", opts.SubtitleStream, len(info.Subtitles))
		}
		switch info.Subtitles[opts.SubtitleStream].Codec {
		case "dvd_subtitle", "dvb_subtitle", "hdmv_pgs_subtitle", "xsub":
			return nil, ErrSubtitleNotText
		}
		tool := opts.FFmpeg
		if tool == "" {
			tool = "ffmpeg"
		}
		data, err := runMediaTool(ctx, tool, maxCueBytes, "-v", "error", "-nostdin", "-protocol_whitelist", "file", "-i", path, "-map", fmt.Sprintf("0:s:%d", opts.SubtitleStream), "-vn", "-an", "-dn", "-c:s", "webvtt", "-f", "webvtt", "pipe:1")
		if err != nil {
			return nil, err
		}
		cues, err = ParseCues(bytes.NewReader(data), CueWebVTT)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i := range cues {
		cues[i].TrackID = strconv.Itoa(opts.SubtitleStream)
	}
	return cues, nil
}
