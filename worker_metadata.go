package ntvideo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"nimbleterminal.dev/ntmediabox"
)

const maxWorkerCueMetadata = 8 << 20

// Versioned application metadata in Track.Kind keeps the ntmediabox v0.1 wire
// unchanged. The host restores the public "subtitles" kind before publishing.
const workerCaptionKindPrefix = "ntvideo/subtitles/v1:"

type workerCueMetadata struct {
	Version int           `json:"version"`
	Cues    *[]CaptionCue `json:"cues"`
}

// decodeWorkerCues validates the application metadata in addition to the
// ntmediabox wire bounds. Never parse the original subtitle as a fallback.
func decodeWorkerCues(data []byte) (*cueTimeline, error) {
	if len(data) == 0 || len(data) > maxWorkerCueMetadata || !utf8.Valid(data) {
		return nil, errors.New("ntvideo: missing or oversized worker cue metadata")
	}
	var meta workerCueMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.Version != 1 || meta.Cues == nil || len(*meta.Cues) > maxCaptionCues {
		return nil, errors.New("ntvideo: invalid worker cue metadata")
	}
	total := 0
	for i, cue := range *meta.Cues {
		total += len(cue.Text)
		if cue.ID != strconv.Itoa(i) || cue.TrackID != "" || cue.Start < 0 || cue.End <= cue.Start || cue.Text != captionPlainText(cue.Text) || total > maxCueBytes {
			return nil, errors.New("ntvideo: invalid worker caption cue")
		}
	}
	return newCueTimeline(*meta.Cues), nil
}

func workerCueHandler(dir string) ntmediabox.RenderFunc {
	return func(req ntmediabox.RenderRequest) (ntmediabox.RenderResponse, error) {
		if req.Kind != "video-caption-cues" || !isBareFilename(req.Name) || len(req.Data) != 0 || len(req.Meta) != 0 {
			return ntmediabox.RenderResponse{}, errors.New("ntvideo: invalid worker cue request")
		}
		path := filepath.Join(dir, req.Name)
		// Check size and file type again inside the worker, before parsing.
		if _, err := validateLocalFile(path, Limits{MaxFileBytes: maxWorkerSidecarBytes}); err != nil {
			return ntmediabox.RenderResponse{}, err
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".srt" && ext != ".vtt" {
			return ntmediabox.RenderResponse{}, errors.New("ntvideo: unsupported worker cue format")
		}
		file := readCueFile(path)
		if file.err != nil {
			return ntmediabox.RenderResponse{}, file.err
		}
		cues := file.timeline.cues
		if cues == nil {
			cues = []CaptionCue{}
		}
		data, err := json.Marshal(workerCueMetadata{Version: 1, Cues: &cues})
		if err != nil {
			return ntmediabox.RenderResponse{}, err
		}
		if _, err := decodeWorkerCues(data); err != nil {
			return ntmediabox.RenderResponse{}, err
		}
		return ntmediabox.RenderResponse{Meta: data}, nil
	}
}

func (b *workerBackend) loadWorkerCues(parent context.Context, dir, video string, caps []ntmediabox.Caption) []nativeCueFile {
	files := make([]nativeCueFile, len(caps))
	// Unsupported sidecars retain the existing "no cue timeline" behavior.
	needsCues := func(cap ntmediabox.Caption) bool {
		ext := strings.ToLower(filepath.Ext(cap.Name))
		return ext == ".srt" || ext == ".vtt"
	}
	needed := false
	for _, cap := range caps {
		needed = needed || needsCues(cap)
	}
	if !needed {
		return files
	}
	conn, closeFn, err := b.starter(b.cfg, dir, video)
	if err != nil {
		for i, cap := range caps {
			if needsCues(cap) {
				files[i].err = err
			}
		}
		return files
	}
	var once sync.Once
	closeHelper := func() {
		once.Do(func() {
			_ = conn.Close()
			if closeFn != nil {
				_ = closeFn()
			}
		})
	}
	defer closeHelper()
	client := ntmediabox.NewRenderClientWithKill(conn, closeHelper)
	defer client.Close()
	ctx, cancel := (HeadlessOptions{}).deadline(parent)
	defer cancel()
	for i, cap := range caps {
		if !needsCues(cap) {
			continue
		}
		resp, err := client.Do(ctx, ntmediabox.RenderRequest{Kind: "video-caption-cues", Name: cap.Name, Width: 1, Height: 1, MaxPixels: 1})
		if err == nil {
			if resp.Ended || resp.Image != nil {
				err = errors.New("ntvideo: invalid worker cue response")
			} else {
				files[i].timeline, err = decodeWorkerCues(resp.Meta)
			}
		}
		if err != nil {
			files[i].err = fmt.Errorf("ntvideo: worker captions: %w", err)
		}
	}
	return files
}

// ParseCuesConfined parses bounded SRT or WebVTT bytes from r inside a
// short-lived OS-confined ServeWorker helper over ntmediabox without loading
// libmpv or parsing subtitle contents in the host process. A zero WorkerConfig
// fails closed with ntmediabox.ErrUnavailable when OS sandboxing is unavailable.
func ParseCuesConfined(ctx context.Context, r io.Reader, format CueFormat, cfg WorkerConfig) ([]CaptionCue, error) {
	tl, err := ParseCueTimelineConfined(ctx, r, format, cfg)
	if err != nil {
		return nil, err
	}
	return tl.Cues(), nil
}

// ParseCueTimelineConfined parses bounded SRT or WebVTT bytes from r inside a
// short-lived OS-confined ServeWorker helper and returns an immutable
// CueTimeline validated by the hostile-peer metadata decoder.
func ParseCueTimelineConfined(ctx context.Context, r io.Reader, format CueFormat, cfg WorkerConfig) (*CueTimeline, error) {
	if runtime.GOOS == "js" {
		return nil, ErrUnsupported
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if format != CueSRT && format != CueWebVTT && format != CueAuto {
		return nil, fmt.Errorf("ntvideo: unsupported cue format %q", format)
	}
	if r == nil {
		return nil, errors.New("ntvideo: nil cue reader")
	}
	dctx, cancel := (HeadlessOptions{}).deadline(ctx)
	defer cancel()

	data, err := io.ReadAll(io.LimitReader(cueContextReader{ctx: dctx, r: r}, maxCueBytes+1))
	if err != nil {
		return nil, err
	}
	ext, err := stageInlineCaptionExt(Caption{Data: data, Format: format}, maxCueBytes)
	if err != nil {
		return nil, err
	}

	var workerPath string
	if cfg.WorkerPath != "" {
		workerPath, err = ntmediabox.VerifyBinary(cfg.WorkerPath)
	} else {
		workerPath, err = ntmediabox.ResolveSibling("ntvideo-worker")
	}
	if err != nil {
		return nil, err
	}

	rawDir, err := os.MkdirTemp("", "ntvideo-cues-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(rawDir) }()
	mediaDir, err := filepath.EvalSymlinks(rawDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(mediaDir) }()
	if err := os.Chmod(mediaDir, 0o700); err != nil {
		return nil, err
	}
	subName := "sub-0" + ext
	if err := writeWorkerBytes(filepath.Join(mediaDir, subName), data, maxCueBytes); err != nil {
		return nil, err
	}

	pol := ntmediabox.DefaultPolicy(workerPath, mediaDir)
	pol.AllowUnsandboxed = cfg.AllowUnsandboxed
	args := make([]string, 0, len(cfg.WorkerArgs)+1)
	args = append(args, slices.Clone(cfg.WorkerArgs)...)
	args = append(args, mediaDir)
	h, err := ntmediabox.StartHelper(ntmediabox.HelperConfig{
		Policy:     pol,
		WorkerPath: workerPath,
		WorkerArgs: args,
		StagePath:  cfg.StagePath,
	})
	if err != nil {
		return nil, err
	}
	conn := &workerConn{ReadWriteCloser: h.Conn, stderr: h.Stderr}
	var once sync.Once
	closeHelper := func() {
		once.Do(func() {
			_ = conn.Close()
			_ = h.Close()
		})
	}
	defer closeHelper()

	client := ntmediabox.NewRenderClientWithKill(conn, closeHelper)
	defer client.Close()

	resp, err := client.Do(dctx, ntmediabox.RenderRequest{
		Kind:      "video-caption-cues",
		Name:      subName,
		Width:     1,
		Height:    1,
		MaxPixels: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("ntvideo: worker captions: %w", err)
	}
	if resp.Ended || resp.Image != nil {
		return nil, errors.New("ntvideo: invalid worker cue response")
	}
	inner, err := decodeWorkerCues(resp.Meta)
	if err != nil {
		return nil, fmt.Errorf("ntvideo: worker captions: %w", err)
	}
	return &CueTimeline{inner: inner}, nil
}
