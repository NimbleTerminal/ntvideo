package ntvideo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"os/exec"
	"strconv"
	"time"
)

// MediaInfo is independent of a terminal and does not require libmpv.
type MediaInfo struct {
	Duration               time.Duration
	Width, Height          int
	VideoCodec, AudioCodec string
	HasAudio               bool
	Subtitles              []SubtitleStream
}

// SubtitleStream describes a container stream. Index is its subtitle-only
// ordinal (0:s:N); StreamIndex is its absolute container stream index.
type SubtitleStream struct {
	Index, StreamIndex     int
	Codec, Language, Title string
	Default, Forced        bool
}

// HeadlessOptions selects trusted tool executables and resource limits. Empty
// paths use ffprobe/ffmpeg from PATH. Each operation has a default 15s deadline.
type HeadlessOptions struct {
	Limits          Limits
	FFprobe, FFmpeg string
	Timeout         time.Duration
	// SubtitleStream selects the zero-based subtitle ordinal for Cues.
	SubtitleStream int
	// MaxFrameWidth/Height bound output images, preserving aspect ratio.
	// Frames defaults to 640x360; FrameAt retains source size when both are zero.
	MaxFrameWidth, MaxFrameHeight int
}

func (o HeadlessOptions) deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	d := o.Timeout
	if d <= 0 {
		d = 15 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

// boundedBuffer stops a subprocess whose output exceeds the expected budget.
type boundedBuffer struct {
	bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, errors.New("ntvideo: tool output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func runMediaTool(ctx context.Context, name string, maxOutput int, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out := &boundedBuffer{max: maxOutput}
	stderr := &boundedBuffer{max: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	// Bound pipe cleanup even when an external tool leaves descendants behind.
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ntvideo: %s: %w: %s", name, err, stderr.String())
	}
	return out.Bytes(), nil
}

// Probe reads local metadata with ffprobe, with network protocols disabled.
func Probe(ctx context.Context, path string, opts HeadlessOptions) (MediaInfo, error) {
	path, err := validateLocalFile(path, opts.Limits)
	if err != nil {
		return MediaInfo{}, err
	}
	ctx, cancel := opts.deadline(ctx)
	defer cancel()
	tool := opts.FFprobe
	if tool == "" {
		tool = "ffprobe"
	}
	data, err := runMediaTool(ctx, tool, 1<<20, "-v", "error", "-protocol_whitelist", "file", "-show_entries", "format=duration:stream=index,codec_type,codec_name,width,height,duration:stream_tags=language,title,handler_name:stream_disposition=default,forced", "-of", "json", path)
	if err != nil {
		return MediaInfo{}, err
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Index    int    `json:"index"`
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
			Tags     struct {
				Language string `json:"language"`
				Title    string `json:"title"`
				Handler  string `json:"handler_name"`
			} `json:"tags"`
			Disposition struct {
				Default int `json:"default"`
				Forced  int `json:"forced"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return MediaInfo{}, err
	}
	duration := func(s string) time.Duration {
		v, _ := strconv.ParseFloat(s, 64)
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > float64((1<<63-1)/int64(time.Second)) {
			return 0
		}
		return time.Duration(v * float64(time.Second))
	}
	info := MediaInfo{Duration: duration(raw.Format.Duration)}
	for _, s := range raw.Streams {
		if s.Type == "subtitle" {
			if s.Tags.Title == "" {
				s.Tags.Title = s.Tags.Handler
			}
			info.Subtitles = append(info.Subtitles, SubtitleStream{Index: len(info.Subtitles), StreamIndex: s.Index, Codec: s.Codec, Language: captionPlainText(s.Tags.Language), Title: captionPlainText(s.Tags.Title), Default: s.Disposition.Default != 0, Forced: s.Disposition.Forced != 0})
		}
		if s.Type == "video" {
			if err = checkDimensions(s.Width, s.Height, opts.Limits); err != nil {
				return MediaInfo{}, err
			}
			if info.VideoCodec == "" {
				info.VideoCodec = s.Codec
				info.Width = s.Width
				info.Height = s.Height
			}
		}
		if s.Type == "audio" {
			info.HasAudio = true
			if info.AudioCodec == "" {
				info.AudioCodec = s.Codec
			}
		}
		if info.Duration == 0 {
			info.Duration = duration(s.Duration)
		}
	}
	return info, nil
}

// FrameAt decodes one frame at or after t without a terminal or libmpv. It uses
// ffprobe then ffmpeg, never plays audio, and returns an owned image. A timestamp
// at/after EOF returns an error. Tool absence is reported to the caller.
func FrameAt(ctx context.Context, path string, t time.Duration, opts HeadlessOptions) (image.Image, error) {
	frames, err := extractFrames(ctx, path, []time.Duration{t}, opts, true)
	if err != nil {
		return nil, err
	}
	return frames[0], nil
}
