package ntvideo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strconv"
	"strings"
	"time"
)

const maxBatchFrames = 16
const maxBatchFramePixels = 16 << 20

// Frames extracts up to 16 frames using one probe and one FFmpeg process, with
// input-side seeks for sparse timestamps. Results match input order, including
// duplicates, and own their pixels independently. Negative times mean zero.
// Output defaults to a 640x360 bounding box, without upscaling; the batch is
// capped at 16 Mi pixels. Source Limits still apply before scaling. Any failure
// (including EOF) returns no partial result. An empty request launches no tools.
func Frames(ctx context.Context, path string, times []time.Duration, opts HeadlessOptions) ([]image.Image, error) {
	return extractFrames(ctx, path, times, opts, false)
}

func extractFrames(ctx context.Context, path string, times []time.Duration, opts HeadlessOptions, fullSize bool) ([]image.Image, error) {
	ctx, cancel := opts.deadline(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(times) == 0 {
		return []image.Image{}, nil
	}
	if len(times) > maxBatchFrames {
		return nil, fmt.Errorf("ntvideo: at most %d frames per batch", maxBatchFrames)
	}
	if opts.MaxFrameWidth < 0 || opts.MaxFrameHeight < 0 {
		return nil, errors.New("ntvideo: frame dimensions must be nonnegative")
	}
	path, err := validateLocalFile(path, opts.Limits)
	if err != nil {
		return nil, err
	}
	info, err := Probe(ctx, path, opts)
	if err != nil {
		return nil, err
	}
	if info.Width <= 0 || info.Height <= 0 {
		return nil, errors.New("ntvideo: source has no video")
	}
	scale := !fullSize || opts.MaxFrameWidth != 0 || opts.MaxFrameHeight != 0
	w, h := opts.MaxFrameWidth, opts.MaxFrameHeight
	if scale {
		if w == 0 {
			w = 640
		}
		if h == 0 {
			h = 360
		}
	} else {
		w, h = info.Width, info.Height
	}
	if err := checkDimensions(w, h, opts.Limits); err != nil {
		return nil, err
	}
	if int64(w) > (maxBatchFramePixels/int64(len(times)))/int64(h) {
		return nil, errors.New("ntvideo: batch output exceeds 16 Mi pixel limit; request smaller frames")
	}
	pixels := int64(w) * int64(h)
	unique := []time.Duration{}
	indices := make([]int, len(times))
	seen := map[time.Duration]int{}
	for i, t := range times {
		t = max(0, t)
		if info.Duration > 0 && t >= info.Duration {
			return nil, fmt.Errorf("ntvideo: timestamp %d is at or past end of video", i)
		}
		index, ok := seen[t]
		if !ok {
			index = len(unique)
			seen[t] = index
			unique = append(unique, t)
		}
		indices[i] = index
	}
	args := []string{"-v", "error", "-nostdin", "-filter_complex_threads", "1"}
	for _, t := range unique {
		args = append(args, "-protocol_whitelist", "file", "-ss", strconv.FormatFloat(t.Seconds(), 'f', 9, 64), "-threads", "1", "-i", path)
	}
	var filters, inputs []string
	for i := range unique {
		filter := fmt.Sprintf("[%d:v:0]trim=end_frame=1,setpts=PTS-STARTPTS", i)
		if scale {
			filter += fmt.Sprintf(",scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease", w, h)
		}
		label := fmt.Sprintf("[frame%d]", i)
		filters = append(filters, filter+label)
		inputs = append(inputs, label)
	}
	filters = append(filters, strings.Join(inputs, "")+fmt.Sprintf("concat=n=%d:v=1:a=0[out]", len(unique)))
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", "-an", "-sn", "-dn", "-frames:v", strconv.Itoa(len(unique)), "-fps_mode", "passthrough", "-threads", "1", "-c:v", "png", "-f", "image2pipe", "pipe:1")
	tool := opts.FFmpeg
	if tool == "" {
		tool = "ffmpeg"
	}
	data, err := runMediaTool(ctx, tool, int(pixels)*len(unique)*8+len(unique)*(1<<20), args...)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(data)
	decoded := make([]image.Image, len(unique))
	for i := range decoded {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data[len(data)-reader.Len():]))
		if err != nil {
			return nil, fmt.Errorf("ntvideo: frame %d: %w", i, err)
		}
		if err := checkDimensions(cfg.Width, cfg.Height, opts.Limits); err != nil {
			return nil, err
		}
		if int64(cfg.Width)*int64(cfg.Height) > pixels || (scale && (cfg.Width > w || cfg.Height > h)) {
			return nil, errors.New("ntvideo: decoded frame exceeds output limits")
		}
		decoded[i], err = png.Decode(reader)
		if err != nil {
			return nil, err
		}
	}
	if reader.Len() != 0 {
		return nil, errors.New("ntvideo: unexpected extra frame output")
	}
	frames := make([]image.Image, len(times))
	used := map[int]bool{}
	for i, index := range indices {
		img := decoded[index]
		if used[index] {
			clone := image.NewNRGBA(img.Bounds())
			draw.Draw(clone, clone.Bounds(), img, img.Bounds().Min, draw.Src)
			img = clone
		}
		frames[i] = img
		used[index] = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return frames, nil
}
