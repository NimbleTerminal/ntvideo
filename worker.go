package ntvideo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nimbleterminal.dev/ntmediabox"
)

const (
	maxWorkerSidecars     = 16
	maxWorkerSidecarBytes = 4 << 20
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

// WorkerConfig configures sandboxed out-of-process media decoding for Model.
type WorkerConfig struct {
	// WorkerPath is the path to the worker executable. When empty, StartHelper
	// resolves "ntvideo-worker" beside os.Executable().
	WorkerPath string
	// WorkerArgs are optional prefix arguments passed to WorkerPath before the
	// private staging directory argument (e.g. []string{"--gloss-media-worker"}).
	WorkerArgs []string
	// StagePath is the Linux pure-Go Landlock + seccomp stage binary. When empty,
	// StartHelper resolves "ntconfine-stage" beside os.Executable().
	StagePath string
	// AllowUnsandboxed permits running the worker process without OS sandbox
	// confinement when OS enforcement is unavailable (such as on Windows).
	// When false (the default), starting the worker fails closed with
	// ntmediabox.ErrUnavailable if OS enforcement is unavailable.
	AllowUnsandboxed bool
}

// WithWorker runs native media decoding in a confined ntmediabox worker process.
// Opt-in caption cue parsing runs in a separate confined helper connection.
// Playback in the worker is software-decoded and silent. It has no effect on
// the browser DOM backend. Volume and mute status retain host preferences;
// changing them never enables worker audio.
func WithWorker(cfg WorkerConfig) Option {
	return func(c *config) {
		c.useWorker = true
		c.worker = WorkerConfig{
			WorkerPath:       cfg.WorkerPath,
			WorkerArgs:       slices.Clone(cfg.WorkerArgs),
			StagePath:        cfg.StagePath,
			AllowUnsandboxed: cfg.AllowUnsandboxed,
		}
	}
}

type workerStarter func(cfg config, mediaDir, resolvedVideo string) (io.ReadWriteCloser, func() error, error)

func createBackend(c config) (backend, error) {
	if c.useWorker && runtime.GOOS != "js" {
		return newWorkerBackend(c)
	}
	return newBackend(c)
}

func defaultWorkerStarter(cfg config, mediaDir, resolvedVideo string) (io.ReadWriteCloser, func() error, error) {
	var workerPath string
	var err error
	if cfg.worker.WorkerPath != "" {
		workerPath, err = ntmediabox.VerifyBinary(cfg.worker.WorkerPath)
	} else {
		workerPath, err = ntmediabox.ResolveSibling("ntvideo-worker")
	}
	if err != nil {
		return nil, nil, err
	}
	libraryPath, err := resolveWorkerLibrary(cfg.libraryPath)
	if err != nil {
		return nil, nil, err
	}
	pol := ntmediabox.DefaultPolicy(workerPath, mediaDir, resolvedVideo, libraryPath)
	pol.AllowUnsandboxed = cfg.worker.AllowUnsandboxed
	args := make([]string, 0, len(cfg.worker.WorkerArgs)+1)
	args = append(args, cfg.worker.WorkerArgs...)
	args = append(args, mediaDir)
	h, err := ntmediabox.StartHelper(ntmediabox.HelperConfig{
		Policy:     pol,
		WorkerPath: workerPath,
		WorkerArgs: args,
		StagePath:  cfg.worker.StagePath,
	})
	if err != nil {
		return nil, nil, err
	}
	return h.Conn, h.Close, nil
}

type workerBackend struct {
	cfg     config
	starter workerStarter

	mu            sync.Mutex
	client        *ntmediabox.Client
	closeHelper   func() error
	mediaDir      string
	session       uint64
	reqGen        uint64
	latestStatus  ntmediabox.Status
	hasStatus     bool
	latestFrame   *image.RGBA
	lastFrame     *image.RGBA
	width, height int
	visible       bool
	sidecars      map[string]nativeCueFile
	cueSeekSerial uint64
	closed        bool
}

func newWorkerBackend(c config) (backend, error) {
	starter := c.workerStart
	if starter == nil {
		starter = defaultWorkerStarter
	}
	return &workerBackend{
		cfg:     c,
		starter: starter,
		width:   480,
		height:  320,
		visible: true,
	}, nil
}

func sanitizeExt(ext string) string {
	ext = strings.ToLower(ext)
	if len(ext) > 10 {
		return ""
	}
	for i, r := range ext {
		if i == 0 {
			if r != '.' {
				return ""
			}
			continue
		}
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return ext
}

func copyWorkerFile(src, dst string, maxBytes int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(dst)
		}
	}()
	n, err := io.Copy(out, io.LimitReader(in, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return errors.New("ntvideo: staged file exceeds size limit")
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func (b *workerBackend) Load(s Source) error {
	s = cloneSource(s)
	resolvedVideo, err := validateLocalFile(s.URL, b.cfg.limits)
	if err != nil {
		return err
	}
	if len(s.Captions) > maxWorkerSidecars {
		return fmt.Errorf("ntvideo: at most %d caption sidecars are supported", maxWorkerSidecars)
	}
	subLimit := b.cfg.limits.normalized()
	subLimit.MaxFileBytes = min(subLimit.MaxFileBytes, maxWorkerSidecarBytes)
	resolvedCaps := make([]string, len(s.Captions))
	sidecars := make([]nativeCueFile, len(s.Captions))
	for i := range s.Captions {
		cpath, err := validateLocalFile(s.Captions[i].Source, subLimit)
		if err != nil {
			return err
		}
		resolvedCaps[i] = cpath
	}

	b.stopHelper()

	rawDir, err := os.MkdirTemp("", "ntvideo-media-")
	if err != nil {
		return err
	}
	mediaDir, err := filepath.EvalSymlinks(rawDir)
	if err != nil {
		_ = os.RemoveAll(rawDir)
		return err
	}
	if err := os.Chmod(mediaDir, 0o700); err != nil {
		_ = os.RemoveAll(mediaDir)
		return err
	}

	videoName := "video" + sanitizeExt(filepath.Ext(resolvedVideo))
	stagedVideo := filepath.Join(mediaDir, videoName)
	if err := stageWorkerVideo(resolvedVideo, stagedVideo, b.cfg.limits.normalized().MaxFileBytes, os.Symlink, os.Link); err != nil {
		_ = os.RemoveAll(mediaDir)
		return err
	}

	if err := writeWorkerSettings(mediaDir, b.cfg.libraryPath); err != nil {
		_ = os.RemoveAll(mediaDir)
		return err
	}

	reqCaps := make([]ntmediabox.Caption, len(s.Captions))
	for i, cap := range s.Captions {
		ext := sanitizeExt(filepath.Ext(resolvedCaps[i]))
		subName := fmt.Sprintf("sub-%d%s", i, ext)
		if err := copyWorkerFile(resolvedCaps[i], filepath.Join(mediaDir, subName), subLimit.MaxFileBytes); err != nil {
			_ = os.RemoveAll(mediaDir)
			return err
		}
		reqCaps[i] = ntmediabox.Caption{
			Name:     subName,
			Label:    cap.Label,
			Language: cap.Language,
			Default:  cap.Default,
		}
	}

	if b.cfg.captionEvents && len(reqCaps) > 0 {
		sidecars = b.loadWorkerCues(context.Background(), mediaDir, resolvedVideo, reqCaps)
	}

	conn, closeFn, err := b.starter(b.cfg, mediaDir, resolvedVideo)
	if err != nil {
		_ = os.RemoveAll(mediaDir)
		return err
	}

	var closeOnce sync.Once
	originalClose := closeFn
	closeFn = func() (err error) {
		closeOnce.Do(func() {
			_ = conn.Close()
			if originalClose != nil {
				err = originalClose()
			}
		})
		return err
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		if closeFn != nil {
			_ = closeFn()
		}
		_ = os.RemoveAll(mediaDir)
		return ErrClosed
	}
	b.session++
	session := b.session
	client := ntmediabox.NewClientWithKill(conn,
		func(ms ntmediabox.Status) { b.onStatus(session, ms) },
		func(f ntmediabox.Frame) { b.onFrame(session, f) }, func() {
			if closeFn != nil {
				_ = closeFn()
			}
		})
	b.client = client
	b.closeHelper = closeFn
	b.mediaDir = mediaDir
	b.sidecars = make(map[string]nativeCueFile, len(reqCaps))
	for i, cap := range reqCaps {
		b.sidecars[cap.Name] = sidecars[i]
	}
	b.latestFrame = nil
	b.lastFrame = nil
	b.reqGen = 1
	b.latestStatus = ntmediabox.Status{
		Gen:   1,
		State: ntmediabox.Loading,
	}
	b.hasStatus = true
	w, h := max(1, b.width), max(1, b.height)
	b.mu.Unlock()

	if err := client.Handshake("video"); err != nil {
		b.stopHelper()
		return err
	}
	if err := client.Resize(w, h); err != nil {
		b.stopHelper()
		return err
	}
	normLimits := b.cfg.limits.normalized()
	req := ntmediabox.OpenRequest{
		Name:     videoName,
		Captions: reqCaps,
		Limits: ntmediabox.Limits{
			MaxFileBytes: normLimits.MaxFileBytes,
			MaxWidth:     normLimits.MaxWidth,
			MaxHeight:    normLimits.MaxHeight,
			MaxPixels:    normLimits.MaxPixels,
		},
		Autoplay:       b.cfg.autoplay,
		Loop:           b.cfg.loop,
		CaptionOverlay: b.cfg.nativeCaptionOverlay,
	}
	if err := client.OpenMedia(req); err != nil {
		b.stopHelper()
		return err
	}
	return nil
}

func (b *workerBackend) onStatus(session uint64, ms ntmediabox.Status) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if session != b.session || b.client == nil || ms.Gen < max(b.reqGen, b.client.Gen()) {
		return
	}
	b.latestStatus = ms
	b.hasStatus = true
}

func (b *workerBackend) onFrame(session uint64, f ntmediabox.Frame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if session != b.session || b.client == nil || f.Gen < max(b.reqGen, b.client.Gen()) || !b.visible || f.Image == nil || f.Image.Bounds().Dx() > b.width || f.Image.Bounds().Dy() > b.height {
		return
	}
	b.latestFrame = f.Image
	b.lastFrame = f.Image
}

func (b *workerBackend) Play() error {
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Play()
}

func (b *workerBackend) Pause() error {
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Pause()
}

func (b *workerBackend) Seek(t time.Duration) error {
	b.mu.Lock()
	c := b.client
	b.cueSeekSerial++
	b.latestFrame = nil
	b.lastFrame = nil
	b.latestStatus.CaptionText = ""
	b.mu.Unlock()
	if c == nil {
		return nil
	}
	if err := c.Seek(t); err != nil {
		return err
	}
	b.mu.Lock()
	b.reqGen = max(b.reqGen, c.Gen())
	b.latestFrame = nil
	b.lastFrame = nil
	b.mu.Unlock()
	return nil
}

func (b *workerBackend) SetVolume(v float64) error {
	b.mu.Lock()
	b.cfg.volume = v
	b.mu.Unlock()
	return nil
}

func (b *workerBackend) SetMuted(v bool) error {
	b.mu.Lock()
	b.cfg.muted = v
	b.mu.Unlock()
	return nil
}

func (b *workerBackend) SelectTrack(kind, id string) error {
	if kind == "audio" || kind == "descriptions" {
		return nil
	}
	b.mu.Lock()
	c := b.client
	b.latestStatus.CaptionText = ""
	b.latestFrame = nil
	b.lastFrame = nil
	b.mu.Unlock()
	if c == nil {
		return nil
	}
	if err := c.SelectTrack(kind, id); err != nil {
		return err
	}
	b.mu.Lock()
	b.reqGen = max(b.reqGen, c.Gen())
	b.latestStatus.CaptionText = ""
	b.latestFrame = nil
	b.lastFrame = nil
	b.mu.Unlock()
	return nil
}

func (b *workerBackend) setCaptionOverlay(enabled bool) error {
	b.mu.Lock()
	b.cfg.nativeCaptionOverlay = enabled
	c := b.client
	b.latestFrame = nil
	b.lastFrame = nil
	b.mu.Unlock()
	if c == nil {
		return nil
	}
	if err := c.SetCaptionOverlay(enabled); err != nil {
		return err
	}
	b.mu.Lock()
	b.reqGen = max(b.reqGen, c.Gen())
	b.latestFrame = nil
	b.lastFrame = nil
	b.mu.Unlock()
	return nil
}

func (b *workerBackend) Layout(_ CellRect, visible bool) error {
	b.mu.Lock()
	b.visible = visible
	if !visible {
		b.width, b.height = 1, 1
		b.latestFrame = nil
	} else if b.latestFrame == nil && b.lastFrame != nil {
		b.latestFrame = b.lastFrame
	}
	c := b.client
	b.mu.Unlock()
	if !visible && c != nil {
		return c.Resize(1, 1)
	}
	return nil
}

func (*workerBackend) SetFit(FitMode) error { return nil }

func (b *workerBackend) Poll() (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := Status{
		State:  Idle,
		Volume: b.cfg.volume,
		Muted:  b.cfg.muted,
	}
	if b.client == nil {
		return s, nil
	}
	ms := b.latestStatus
	s.State = fromWorkerState(ms.State)
	s.CurrentTime = ms.Position
	s.Duration = ms.Duration
	s.CaptionText = captionPlainText(ms.CaptionText)
	if len(ms.CaptionTracks) > 0 {
		s.CaptionTracks = make([]Track, len(ms.CaptionTracks))
		for i, tr := range ms.CaptionTracks {
			kind := tr.Kind
			if strings.HasPrefix(kind, workerCaptionKindPrefix) {
				kind = "subtitles"
			}
			s.CaptionTracks[i] = Track{
				ID:       tr.ID,
				Label:    tr.Label,
				Language: tr.Language,
				Kind:     kind,
				Selected: tr.Selected,
			}
		}
	}
	if ms.Err != "" {
		s.State = Failed
		s.Err = errors.New(ms.Err)
		return s, s.Err
	}
	select {
	case <-b.client.Dead():
		s.State = Failed
		if cerr := b.client.Err(); cerr != nil {
			s.Err = fmt.Errorf("ntvideo: media worker exited: %w", cerr)
		} else {
			s.Err = errors.New("ntvideo: media worker exited unexpectedly")
		}
		return s, s.Err
	default:
	}

	if b.cfg.captionEvents && ms.Gen >= max(b.reqGen, b.client.Gen()) && (s.State == Playing || s.State == Paused || s.State == Buffering || s.State == Ended) {
		snap := &cueSnapshot{seekSerial: b.cueSeekSerial}
		s.cueData = snap
		for i, tr := range s.CaptionTracks {
			if !tr.Selected {
				continue
			}
			snap.track = tr.ID
			name, identified := strings.CutPrefix(ms.CaptionTracks[i].Kind, workerCaptionKindPrefix)
			if !identified {
				if len(b.sidecars) > 0 {
					s.CaptionCueErr = errors.New("ntvideo: worker did not identify caption source; rebuild helper")
				}
			} else if name != "" {
				if file, ok := b.sidecars[name]; ok && isBareFilename(name) {
					snap.timeline = file.timeline
					s.CaptionCueErr = file.err
					s.CaptionCuesAvailable = file.timeline != nil
				} else {
					s.CaptionCueErr = errors.New("ntvideo: unknown worker caption source")
				}
			}
			break
		}
	}
	return s, nil
}

func (b *workerBackend) Frame(w, h int) (image.Image, error) {
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	w = min(w, ntmediabox.MaxFrameSide)
	h = min(h, ntmediabox.MaxFrameSide)

	b.mu.Lock()
	if !b.visible || b.client == nil {
		b.mu.Unlock()
		return nil, nil
	}
	needResize := w != b.width || h != b.height
	if needResize {
		b.width, b.height = w, h
		b.latestFrame = nil
	}
	img := b.latestFrame
	b.latestFrame = nil
	c := b.client
	b.mu.Unlock()

	if needResize && c != nil {
		_ = c.Resize(w, h)
	}
	if img == nil {
		return nil, nil
	}
	return img, nil
}

func (b *workerBackend) stopHelper() {
	b.mu.Lock()
	c := b.client
	closeFn := b.closeHelper
	dir := b.mediaDir
	b.session++
	b.client = nil
	b.closeHelper = nil
	b.mediaDir = ""
	b.latestFrame = nil
	b.lastFrame = nil
	b.mu.Unlock()

	if c != nil {
		_ = c.Close()
	}
	if closeFn != nil {
		_ = closeFn()
	}
	if dir != "" {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
		_ = os.Remove(dir)
	}
}

func (b *workerBackend) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.stopHelper()
	return nil
}

func fromWorkerState(s ntmediabox.State) State {
	switch s {
	case ntmediabox.Loading:
		return Loading
	case ntmediabox.Buffering:
		return Buffering
	case ntmediabox.Playing:
		return Playing
	case ntmediabox.Paused:
		return Paused
	case ntmediabox.Ended:
		return Ended
	case ntmediabox.Failed:
		return Failed
	default:
		return Idle
	}
}

func toWorkerState(s State) ntmediabox.State {
	switch s {
	case Loading:
		return ntmediabox.Loading
	case Buffering:
		return ntmediabox.Buffering
	case Playing:
		return ntmediabox.Playing
	case Paused:
		return ntmediabox.Paused
	case Ended:
		return ntmediabox.Ended
	case Failed:
		return ntmediabox.Failed
	default:
		return ntmediabox.Idle
	}
}

// ServeWorker runs the worker side of an ntmediabox connection for kind "video",
// reading files only by bare name from dir with hardware decoding and audio off.
// It also serves bounded caption-cue metadata over the ntmediabox render protocol
// on a separate connection, without loading libmpv.
func ServeWorker(conn io.ReadWriter, dir string, opts PlayerOptions) error {
	return serveWorker(conn, dir, func(kind string) (ntmediabox.Decoder, error) {
		if kind != "video" {
			return nil, errors.New("ntvideo: unknown worker media kind")
		}
		return NewWorkerDecoder(dir, opts), nil
	})
}

// serveWorker selects the documented ntmediabox media or render protocol once
// per connection. Render requests carry caption metadata; media remains unchanged.
func serveWorker(conn io.ReadWriter, dir string, factory func(string) (ntmediabox.Decoder, error)) error {
	reader := bufio.NewReader(conn)
	header, err := reader.Peek(5)
	if err != nil {
		return err
	}
	stream := struct {
		io.Reader
		io.Writer
	}{reader, conn}
	// 0x20 is msgRenderReq in the ntmediabox render wire specification.
	if header[4] == 0x20 {
		return ntmediabox.ServeRender(stream, workerCueHandler(dir))
	}
	return ntmediabox.Serve(stream, factory)
}

// NewWorkerDecoder returns an ntmediabox.Decoder backed by the native media
// backend in dir with hardware decoding and audio disabled. opts.Limits sets
// the worker's maximum limits; requests may narrow them. Use ServeWorker to
// support caption-cue metadata as well as the streaming decoder protocol.
func NewWorkerDecoder(dir string, opts PlayerOptions) ntmediabox.Decoder {
	opts.DisableHardwareDecoding = true
	opts.DisableAudio = true
	return &workerDecoder{
		dir:    dir,
		opts:   opts,
		create: newBackend,
	}
}

type workerDecoder struct {
	dir          string
	opts         PlayerOptions
	create       func(config) (backend, error)
	mu           sync.Mutex
	b            backend
	closed       bool
	captionNames map[string]bool
}

func isBareFilename(name string) bool {
	if name == "" || len(name) > 255 || name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) || name == "." || name == ".." || strings.HasPrefix(name, "-") || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return false
		}
	}
	return true
}

func (d *workerDecoder) Load(req ntmediabox.OpenRequest) error {
	if !isBareFilename(req.Name) {
		return errors.New("ntvideo: bad media name")
	}
	if len(req.Captions) > maxWorkerSidecars {
		return errors.New("ntvideo: too many worker captions")
	}
	caps := make([]Caption, len(req.Captions))
	for i, c := range req.Captions {
		if !isBareFilename(c.Name) {
			return errors.New("ntvideo: bad caption name")
		}
		caps[i] = Caption{
			Source:   filepath.Join(d.dir, c.Name),
			Label:    c.Label,
			Language: c.Language,
			Default:  c.Default,
		}
	}
	lim := d.opts.Limits.normalized()
	if req.Limits.MaxFileBytes > 0 {
		lim.MaxFileBytes = min(lim.MaxFileBytes, req.Limits.MaxFileBytes)
	}
	if req.Limits.MaxWidth > 0 {
		lim.MaxWidth = min(lim.MaxWidth, req.Limits.MaxWidth)
	}
	if req.Limits.MaxHeight > 0 {
		lim.MaxHeight = min(lim.MaxHeight, req.Limits.MaxHeight)
	}
	if req.Limits.MaxPixels > 0 {
		lim.MaxPixels = min(lim.MaxPixels, req.Limits.MaxPixels)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if d.b != nil {
		_ = d.b.Close()
		d.b = nil
	}
	libraryPath, err := workerLibraryPath(d.dir, d.opts.LibraryPath)
	if err != nil {
		return err
	}
	d.captionNames = make(map[string]bool, len(req.Captions))
	for _, cap := range req.Captions {
		d.captionNames[cap.Name] = true
	}
	b, err := d.create(config{
		libraryPath:          libraryPath,
		volume:               1,
		muted:                true,
		disableHW:            true,
		disableAudio:         true,
		loop:                 req.Loop,
		autoplay:             req.Autoplay,
		nativeCaptionOverlay: req.CaptionOverlay,
		limits:               lim,
	})
	if err != nil {
		return err
	}
	d.b = b
	return d.b.Load(Source{
		URL:      filepath.Join(d.dir, req.Name),
		Captions: caps,
	})
}

func (d *workerDecoder) with(fn func(backend) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if d.b == nil {
		return nil
	}
	return fn(d.b)
}

func (d *workerDecoder) Play() error  { return d.with(backend.Play) }
func (d *workerDecoder) Pause() error { return d.with(backend.Pause) }
func (d *workerDecoder) Seek(t time.Duration) error {
	return d.with(func(b backend) error { return b.Seek(t) })
}

func (d *workerDecoder) SelectTrack(kind, id string) error {
	return d.with(func(b backend) error {
		if err := b.SelectTrack(kind, id); err != nil {
			return err
		}
		_ = b.Layout(CellRect{}, true)
		return nil
	})
}

func (d *workerDecoder) SetCaptionOverlay(enabled bool) error {
	return d.with(func(b backend) error {
		if ov, ok := b.(interface{ setCaptionOverlay(bool) error }); ok {
			return ov.setCaptionOverlay(enabled)
		}
		return nil
	})
}

func (d *workerDecoder) Status() (ntmediabox.Status, error) {
	var ms ntmediabox.Status
	ms.State = ntmediabox.Idle
	err := d.with(func(b backend) error {
		s, err := b.Poll()
		if err == nil && s.Err != nil {
			err = s.Err
		}
		ms.State = toWorkerState(s.State)
		ms.Position = s.CurrentTime
		ms.Duration = s.Duration
		ms.CaptionText = s.CaptionText
		if len(s.CaptionTracks) > 0 {
			ms.CaptionTracks = make([]ntmediabox.Track, len(s.CaptionTracks))
			for i, tr := range s.CaptionTracks {
				kind := tr.Kind
				if path, ok := s.captionFiles[tr.ID]; ok {
					if path == "" {
						kind = workerCaptionKindPrefix
					} else {
						name := filepath.Base(path)
						if d.captionNames[name] && filepath.Clean(path) == filepath.Join(d.dir, name) {
							kind = workerCaptionKindPrefix + name
						}
					}
				}
				ms.CaptionTracks[i] = ntmediabox.Track{
					ID:       tr.ID,
					Label:    tr.Label,
					Language: tr.Language,
					Kind:     kind,
					Selected: tr.Selected,
				}
			}
		}
		return err
	})
	return ms, err
}

func (d *workerDecoder) Frame(w, h int) ([]byte, int, int, error) {
	var pix []byte
	var fw, fh int
	err := d.with(func(b backend) error {
		img, err := b.Frame(w, h)
		if err != nil || img == nil {
			return err
		}
		pix, fw, fh = pixelsToRGBA(img)
		return nil
	})
	return pix, fw, fh, err
}

func (d *workerDecoder) Unload() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.b == nil {
		return nil
	}
	err := d.b.Close()
	d.b = nil
	return err
}

func (d *workerDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if d.b == nil {
		return nil
	}
	return d.b.Close()
}

func pixelsToRGBA(img image.Image) ([]byte, int, int) {
	b := img.Bounds()
	switch v := img.(type) {
	case *image.RGBA:
		if v.Stride == 4*b.Dx() && len(v.Pix) == 4*b.Dx()*b.Dy() {
			return v.Pix, b.Dx(), b.Dy()
		}
	case *image.NRGBA:
		if v.Stride == 4*b.Dx() && len(v.Pix) == 4*b.Dx()*b.Dy() {
			return v.Pix, b.Dx(), b.Dy()
		}
	}
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return rgba.Pix, b.Dx(), b.Dy()
}
