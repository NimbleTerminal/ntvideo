# ntvideo

**[Try the live browser demo](https://nimbleterminal.github.io/ntvideo/)**

A Bubble Tea v2 video component for Golang Terminal applications. Browsers use a
real HTML video element; native builds load libmpv at runtime for playback and
`ntpicture.Picture` for Kitty graphics or
half-block rendering. Audio belongs to the browser or the OS media backend.

This is an experimental MVP, with a portable public API offering native and browser playback.

For v0.2.0 changes, see the [changelog](CHANGELOG.md) and
[migration guide](MIGRATING.md), including the `Model.Close` API change.

## Use

```go
import "nimbleterminal.dev/ntvideo"

v := ntvideo.New(
    ntvideo.WithSource("demo.mp4"),
    ntvideo.WithWidth(60),
    ntvideo.WithHeight(20),
    ntvideo.WithFit(ntvideo.FitContain),
    ntvideo.WithScrubber(true),
    ntvideo.WithDescription("A demonstration of the product"),
)
```

The component follows `ntpicture.Picture`'s pointer-mutating convention:

- Return `v.Init()` from the parent model's `Init`.
- Forward **every** Bubble Tea message to `v.Update(msg)` and run its returned command.
- Compose `v.View()` into the parent's `tea.View.Content`.
- Call `v.Play()`, `v.Pause()`, `v.Seek(duration)`, `v.SetVolume(0.5)`, or
  `v.SetMuted(true)` on the Tea event loop. They queue work in call order; they
  need no returned command. State is asynchronous: read `v.Status()` or consume
  `ntvideo.EventMsg`, emitted when `Status` changes, using its ID to distinguish widgets.
- Run commands returned by `SetBounds`, `SetSize`, and `SetVisible`. Positions
  are zero-based terminal **cells**, relative to the host's visible grid.
- Before quitting, return `tea.Sequence(v.Close(), tea.Quit)`. Also defer
  `v.Close()` around `Program.Run()` to release resources on cancellation and
  errors (terminal output requires running the returned command). `Shutdown`
  remains an alias for `Close`. `Close` now returns `tea.Cmd`, not `error`.

The parent owns keyboard bindings and focus policy. `Focus`, `Blur`, and
`Focused` let it track selection; the component does not intercept global keys.
The example maps space, arrows, mute, and volume controls.

Native seeks requested while a file is loading are held until mpv reports the
file loaded. The latest target wins, without changing the paused/playing state;
loading another source discards the previous target. Hosts can restore a saved
position immediately after `Load`, without waiting for `Status().Duration`.

Fit modes are `FitContain` (letterbox), `FitCover` (center crop), and `FitFill`
(stretch). Contain is the default. `v.SetFit(ntvideo.FitCover)` changes fit without
restarting playback; run its returned command. Press **f** in the example to cycle
through all three modes, including while paused. Fit applies within the widget
rectangle. Press **Enter** in the example to toggle between the fixed 60×20
region and the available terminal area; the status shows the current dimensions.

`WithScrubber(true)` reserves the last row **inside** the widget's configured
height for a progress bar and elapsed/total time. Click the bar to seek, or drag
the thumb and release to seek to the previewed time. The parent must set
`tea.View.MouseMode = tea.MouseModeCellMotion`, forward mouse messages to
`v.Update`, and keep `SetBounds` aligned with the widget's actual position.
The example already does this. Unknown durations disable seeking; small widths
omit timestamps. Hiding or resizing the widget cancels an active drag. Browser
native controls are enabled by default; hosts can opt out as described below.

## Glyph or raster rendering

Native rendering defaults to `auto`: Kitty raster graphics when detected,
otherwise ANSI half-block glyphs. Choose a mode on startup:

```sh
task video -- -render glyph '/path/to/video.mp4'
task video -- -render raster '/path/to/video.ogg'
```

Press **r** in the native player to cycle auto → glyph → raster. Raster requires
a Kitty-graphics-compatible terminal. While detection is pending, video stays
in glyph mode and switches automatically when support is confirmed. Unsupported
terminals keep glyph playback without a playback error. The status shows the requested mode and
active transport. Switching also redraws a paused frame.

Hosts can use `WithRenderMode(ntvideo.RenderGlyph)` at construction, or return
`v.SetRenderMode(ntvideo.RenderRaster)` from Update. `RenderAuto` restores
detection. These options affect native terminals; browsers use HTML video.

## Host integration and lifecycle

Playback starts **paused on the first frame, with sound enabled**. The host owns
Space and all other keys. `WithAutoplay(true)` opts into automatic playback.
`SetVisible(false)` pauses playback and hides the picture; showing it again does
not resume sound. Call `Play()` explicitly to resume. Browser overlays also pause
when their host, document, or visible region disappears.

`Position()`, `Duration()`, and `Status()` expose timing for a host-owned seek bar.
The built-in scrubber is optional. Native frame preparation is asynchronous for
both glyphs and Kitty. Sampling waits for `ntpicture.PresentedMsg`; generation
checks discard stale work after seeking, resizing, hiding, or loading. Image IDs
come from `ntpicture.NextImageID`, with stable IDs for unchanged frame geometry.

Share the same terminal session and allocator with other pictures:

```go
session := ntpicture.NewTerminalSession(ntpicture.TerminalOptions{})
v := ntvideo.New(
    ntvideo.WithTerminalSession(session),
    ntvideo.WithImageIDAllocator(ntpicture.NextImageID),
    ntvideo.WithSource("movie.mp4"),
)
// Host Init: return tea.Batch(session.Init(), v.Init())
// Forward all terminal and picture messages to v.Update and run its commands.
// The host closes session after all its components are finished.
```

`Keep()` releases playback resources and leaves the last committed frame on the
current screen. `KeepOnMainScreen()` does the same while leaving the alternate
screen: set the host view's `AltScreen` to false, include `v.View()` in that final
view, and return `tea.Sequence(v.KeepOnMainScreen(), tea.Quit)`. Continue forwarding
late messages to `Update`, including after closing. Closing after Keep preserves
the retained picture. `Release() error` stops only media resources, without
terminal output; use Close/Keep for the complete picture lifecycle.

## Headless metadata, transcripts, and frames

These APIs need no terminal, libmpv, or audio device. `Probe` uses **ffprobe**;
container subtitle extraction and frame decoding also use **ffmpeg**. Sidecar
parsing needs neither tool. File APIs accept local regular files only, disable
network protocols in external tools, bound their output, and impose a 15-second
default deadline for the whole operation (also honoring context cancellation).

```go
info, err := ntvideo.Probe(ctx, "movie.mp4", ntvideo.HeadlessOptions{})
// info: Duration, Width, Height, VideoCodec, AudioCodec, HasAudio, Subtitles
frame, err := ntvideo.FrameAt(ctx, "movie.mp4", time.Second, ntvideo.HeadlessOptions{})
// frame is an owned image.Image, suitable for PNG output, thumbnails or WithPoster.
```

`HeadlessOptions` accepts `Limits`, `Timeout`, and trusted executable paths
`FFprobe`/`FFmpeg`. Missing tools and timestamps at/past EOF return errors. The
viewer does not automatically spawn FFmpeg for a fallback; a host can obtain a
poster with FrameAt and pass it through WithPoster. Without a poster or libmpv,
the widget shows “install mpv to play video”.

### Subtitle discovery and transcripts

`info.Subtitles` lists each subtitle stream's `Index` (subtitle ordinal for
`0:s:N`), `StreamIndex` (absolute container index), `Codec`, `Language`, `Title`,
`Default`, and `Forced`. Titles fall back to the container's handler name when
there is no title tag. Use the subtitle ordinal to select a transcript:

```go
cues, err := ntvideo.Cues(ctx, "movie.mp4", ntvideo.HeadlessOptions{
    SubtitleStream: 0, // First subtitle stream; choose via Probe's Subtitles list.
})
// After checking err, print a transcript for --text or an LLM:
for _, cue := range cues {
    fmt.Printf("%s --> %s\n%s\n\n", cue.Start, cue.End, cue.Text)
}
```

`Cues` reads `.srt`/`.vtt` files directly and converts container subtitle streams
to WebVTT with FFmpeg. It returns `ErrNoSubtitles` for containers without subtitle
streams and `ErrSubtitleNotText` for known bitmap subtitle codecs; it does not
perform OCR or speech recognition. Other unsupported codecs return FFmpeg errors.
These headless transcripts do not automatically enable embedded native playback
cue events. `TrackID` is the chosen subtitle ordinal, independently of libmpv IDs.

For an existing reader, use the exported parser:

```go
cues, err := ntvideo.ParseCues(reader, ntvideo.CueSRT)
// Or ntvideo.CueWebVTT for a WebVTT reader, including its WEBVTT header.
```

The parser and transcript extraction accept at most 4 MiB and 10,000 cues, with
4096 sanitized plain-text runes per cue. IDs are stable ordinals in file order;
multiline text and timestamps are retained, while styling is stripped. Inputs
must be UTF-8; WebVTT timestamp maps are unsupported. Errors return no partial
transcript. `ParseCues` leaves its reader open; callers own cancellation of a
blocking reader. It leaves `TrackID` empty because no stream was selected.

### Batch thumbnails

```go
frames, err := ntvideo.Frames(ctx, "movie.mp4", []time.Duration{
    10 * time.Minute, 40 * time.Minute, 90 * time.Minute,
}, ntvideo.HeadlessOptions{MaxFrameWidth: 640, MaxFrameHeight: 360})
```

`Frames` uses one probe and one FFmpeg process, with a separate input-side seek
for each distinct timestamp. It decodes near the requested times rather than
scanning the intervening movie, scales before PNG encoding, and returns owned
images in request order. Duplicate timestamps produce independent images.
Codec decoding may still require full-resolution source frames.

The default output box is 640×360, preserving frame aspect ratio without
upscaling. Batches accept up to 16 requests and a total output bounding-box budget
of 16 Mi pixels. Source file/resolution `Limits` still apply before scaling;
increase those explicitly for larger sources. Negative timestamps mean zero;
timestamps at/past EOF fail the entire batch. Empty requests launch no tools.
`FrameAt` accepts the same output-size options and shares the bounded extraction
path; with both dimensions zero it retains the source size. A zero dimension in
an explicitly resized request uses that dimension's 640×360 default.

## Task runner

The Taskfile follows the sibling ntcharts repositories: `default` builds,
`build-ex-*` writes to `bin/`, and `build-wasm-site` / `serve-wasm-site` manage
the browser example. Run `task list` for all tasks.

```sh
task                                      # library + native libmpv player
task video -- '/path/with spaces/movie.mp4' # build and play (aliases: player, run)
task build-fallback                       # compatibility name; runtime loader build
task test -- -race                        # both Go modules
task test-native -- -race                 # both modules; media tests are opt-in
task test-media SOURCE='/path/to/movie.mp4'
task ci                                  # portable preflight, no source rewrites
task ci-native                           # libmpv + real playback checks
task ci-full                             # also Chrome (run task dev-deps first)
task vet
task go-tidy

task serve-wasm-site                      # copy a video to dist/demo.mp4 first
PORT=9090 task serve-wasm-site             # custom port
task build-pages                          # dist/ for GitHub Pages; generates a demo clip if absent
task test-web
task test-wasm                            # DOM backend tests under Node
task dev-deps                             # install browser test dependencies
task test-browser                        # generates its own 4-second test clip

task clean                               # preserves user media in dist/
```

Native builds use goffi and support `CGO_ENABLED=0` on macOS, Linux, and Windows
(amd64/arm64). libmpv is needed only at playback time, not to start the host.
No purego dependency is added. Browser integration tests require installed
Chrome and FFmpeg with libx264. Build tasks do not automatically update or tidy
module dependencies.

`task ci` fails on formatting, untidy modules, checksum mismatches, whitespace,
race-test failures, vet errors, JavaScript syntax/test failures, or build failures.
It checks both Go modules, vets WASM, builds Linux/Windows players with `CGO_ENABLED=0`, and
assembles the WASM demo. It requires Go with race-detector support, Git, Node/npm,
and a POSIX shell; it does not require libmpv, FFmpeg, Chrome, or Playwright.
It writes only build outputs/caches and does not run formatting or module fixes.
Use `task fmt` or `task go-tidy` to fix their respective checks.

`task ci-native` adds runtime-loader builds/vet/race tests and playback tests using a
generated FFmpeg clip. `task ci-full` runs both preflights plus the Chrome
integration suite. Missing dependencies fail explicitly rather than skipping
checks. GitHub Actions runs `task ci` and `task ci-native` on every push and pull
request, runs the Chrome suite in a separate job, and publishes the browser demo
to GitHub Pages from `main` with `task build-pages`.

## Native example

Go 1.26.8 or newer is required.

```sh
# macOS
brew install mpv
# Debian/Ubuntu: the mpv player package alone does not supply libmpv.
sudo apt install libmpv2 ffmpeg
```

On Windows, install a trusted libmpv DLL. FFmpeg is needed for headless metadata
and frame extraction, not native playback. Then run:

```sh
cd examples/player
go run . /path/to/demo.mp4
```

The example starts as a bordered player filling the terminal, with a filename
header and a control panel beneath the video. Click Play/Pause, ±5s, fit, and size
buttons. Volume has clickable −/+ buttons, a draggable slider, a percentage, and
Mute/Unmute. Keyboard controls remain available.

Press Enter for the centered small player with a 60×20 video/scrubber region
(19 video rows and one scrubber row), or Enter again to expand. Borders and
controls add two columns and eight rows. Both layouts shrink with the terminal;
windows smaller than 28×12 show a resize hint. Press space to play. Kitty support is probed by picture; other terminals use
half-blocks. Under tmux, enable passthrough as required by ntpicture.
Native audio plays on the machine running the Go process, including over SSH.

Kitty frames use shared memory by default. picture asks the terminal whether it
can read a shared buffer; when it answers OK, each frame is a raw RGBA copy into
a private object (random name, mode 0600) and only a short command crosses the
tty. That skips a PNG encode of roughly 14 ms per 960×540 frame and hundreds of
kilobytes of stream bandwidth. Terminals without shared memory, remote sessions,
and sandboxes that cannot share a namespace fall back to direct PNG frames
automatically. The player status line shows the active transport, which
`Model.Transport()` also reports (`kitty/shm`, `kitty/png`, `glyph`, `dom`).
`WithKittyMedium(ntpicture.KittyMediumDirect)` forces direct transmission.

No build tag, C compiler, mpv headers, or pkg-config is required. The loader
searches for libmpv's platform library names (and standard Homebrew paths on
macOS). `WithLibraryPath` selects a trusted explicit library. An unavailable
library is reported through `Status.Err` as `ErrUnsupported`, without preventing
the host from starting. `Capabilities()` reports backend support for this build;
it deliberately does not load libmpv. Successful playback is the runtime check.

## Browser example

Try the [hosted demo](https://nimbleterminal.github.io/ntvideo/), or run it locally:

```sh
./scripts/build-web.sh
cp /path/to/browser-compatible.mp4 dist/demo.mp4
node scripts/serve-web.mjs
# Open http://localhost:8080/index.html
```

The included local server supports byte ranges for seeking. Use another range-capable
static server if preferred.

Use `?source=https://example.com/movie.mp4` for another source. Server policies,
codec support, range requests, and cross-origin caption access must permit that
source. `Source.URL` also accepts relative same-origin URLs and `blob:` URLs
created by the host for embedded or dropped files. ntvideo assigns the URL to the
video element; it does not fetch or copy the bytes. The **host owns each blob URL**
and calls `URL.revokeObjectURL(url)` after `Close()` has removed the video (or after
replacing the source). ntvideo never revokes URLs. Native sources remain local
files only.

Browser-native controls are on by default. Use `WithNativeControls(false)` when
the terminal owns input: the video has no native controls, `tabIndex=-1`, and
`pointer-events: none`. The host **must provide accessible keyboard controls**
and an appropriate description; the video retains its `aria-label` and the
surface retains its `role=status` text. `WithScrubber(true)` still draws and handles
the terminal scrubber row from `Status`, including click-and-drag seeking.
The example uses this mode: click the terminal and press Space to play/pause,
or use its terminal controls. Autoplay and programmatic play can be rejected by
browser policy; rejection is reported through `Status.Err`.

The build copies go-booba's pinned browser assets from its Go module cache. The
library does not depend on go-booba. The example's separate module does.
Upstream Bubble Tea v2 currently lacks the required js/wasm platform files; this
repository and go-booba use the same fork. **Downstream WASM applications must
include this replacement in their own go.mod**, because Go does not inherit
replacements from dependencies:

```go
replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20261009201241-c397c32a5074
```

Publish the browser module without depending on the player or Booba:

```go
// In the host module's go.mod (alongside its ntvideo requirement):
tool nimbleterminal.dev/ntvideo/cmd/ntvideo-assets
```

```sh
go tool ntvideo-assets dist --force
# Refresh assets whenever the host upgrades ntvideo.
# Omit --force to preserve existing files.
```

```html
<script type="module">
  import {registerBoobaHost} from './ntvideo/surface.js';
  // After await booba.init(), before starting WASM:
  const unregister = registerBoobaHost('terminal', booba);
  // Register before starting WASM; call unregister when disposing the terminal.
</script>
```

`registerBoobaHost` accepts the initialized `BoobaTerminal` instance. It measures
CSS cells and clips to the terminal canvas/container, tracks page movement and
resize, and hides/pauses video in scrollback or outside the alternate screen.
Other terminal hosts can use `registerHost(id, measure)`. The global bridge is
`ntvideoHost`; `ntchartsVideoHost` remains an alias for older WASM clients.
Construct the widget with `WithHost("terminal")` to match the registered ID.

`nimbleterminal.dev/ntvideo/web.Assets` is also an embedded `fs.FS` for hosts that
serve assets directly. It contains runtime files only.

Load `ntvideo/surface.js` and register a host before initializing the widget. See
`web/index.html` for the go-booba integration. The host measures the content
canvas in CSS pixels and supplies clipping and visibility. Applications must
call `SetBounds` whenever layout changes; strings alone cannot reveal a widget's
location in an arbitrarily composed terminal view.

The supplied host supports the alternate screen, hides overlays while viewing
scrollback, and tracks viewport resizing and page movement. Arbitrary inline
scrollback anchoring/reflow is **not** implemented. Such hosts must track an
anchor or return `visible: false` when placement is uncertain. Modal interfaces
must hide covered widgets or coordinate z-index with the host.

## Accessibility and capabilities

The example uses **c** to toggle captions (restoring the last selected track)
and **C** to open the caption menu. Use Up/Down and Enter to select a track or
Off; Esc cancels. The **CC on/off** button opens the same menu when space allows.
Opening the menu pauses and hides video; closing it does not resume playback.

Supply external captions explicitly, or select embedded native subtitle tracks:

```sh
task video -- -captions '/path/to/english.vtt' -captions '/path/to/spanish.srt' '/path/to/movie.mp4'
```

For the browser example, use
`?source=demo.mp4&captions=english.vtt&captions=spanish.vtt` (URL-encode paths
as needed). Browser sidecars must be WebVTT and satisfy browser access rules.
The example does not mark a sidecar as the default; use the menu to choose.

For native playback, press **t** to switch between backend-rendered captions and
plain terminal text over the video, independently of the video's **r** rendering
mode. Start with terminal text using:

```sh
task video -- -caption-render text -captions '/path/to/subtitles.srt' '/path/to/movie.mp4'
```

The text overlay uses up to three centered, wrapped lines with a black background
above the scrubber. It works with glyph video and Kitty placeholders. Text comes
from libmpv's active cue, so playback, pause, and seek retain subtitle timing.
`WithNativeCaptionOverlay(true)` and `SetNativeCaptionOverlay(bool)` expose this
option to hosts; the setter queues work like `Play`. Browsers retain their HTML
caption rendering. Bitmap subtitle tracks have no text equivalent; use backend
rendering for those. ASS styling and positioning are omitted in terminal text mode.

`WithMedia(ntvideo.Source{URL: ..., Captions: []ntvideo.Caption{...}})` adds caption
tracks. Browsers use WebVTT `<track>` elements and native video controls. Native
libmpv accepts external subtitle files and renders subtitles into the image.
`Status` exposes audio/caption tracks; `SelectAudioTrack` and
`SelectCaptionTrack` select them (an empty caption ID disables captions).
Audio-description tracks are ordinary selectable media audio tracks; native
metadata identifies them when available. Browser audio-track selection is
feature-detected and may be unavailable.

`WithDescription` labels browser video and status; `WithTranscript` exposes a
browser transcript link. Terminal applications should present descriptive status
and transcript links as ordinary text beside the image. Burned-in native
subtitles are not screen-reader text. There is no synthesized audio description
or transcript extraction. `Status.CaptionText` exposes the current native
plain-text cue (sanitized and bounded), and cue changes emit `EventMsg` even
while paused. It is empty between cues, for bitmap subtitles, and in browsers.
Hosts can use it in their own terminal text or accessibility interface.

### Caption cue events

Opt in with `WithCaptionEvents(true)` to receive `CaptionCueMsg` on the Tea loop.
Each event contains `VideoID`, `SourceID`, `Cue` (`ID`, `TrackID`, `Start`, `End`,
`Text`), `Kind` (`CueEnter`/`CueExit`), and `Reason`. A cue may contain multiple
lines. Consecutive cues with identical text still have distinct IDs.

```go
// Parent Update: keep forwarding messages and run actions as commands.
cmd := m.video.Update(msg)
if event, ok := msg.(ntvideo.CaptionCueMsg); ok &&
    event.VideoID == m.video.ID() && event.Kind == ntvideo.CueEnter &&
    event.Reason == ntvideo.CuePlayback {
    cue := event.Cue
    return m, tea.Batch(cmd, func() tea.Msg {
        return handleCaption(cue) // Your application function returns a Tea message.
    })
}
return m, cmd
```

Also handle `CueLoad` and `CueTrackChange` enters if the action should run for a
cue already active when a file loads or a track is selected. The example above
intentionally runs only for boundaries crossed during playback.

| Operation | Cue events |
| --- | --- |
| Forward playback | Every crossed start/end, including short cues between samples; equal-time exits precede enters |
| Pause/hide | No repeated events for an unchanged active cue |
| `Seek` | Exit old cues with `CueSeek`; enter destination cues once the seek settles; skip intermediate cues |
| Caption selection/off | Exit old cues with `CueTrackChange`; enter any active cues on the new track |
| `Load` | Exit old-source cues with `CueLoad`; increment `SourceID`; enter active new-source cues when ready |
| `Close`/`Keep` | Exit active cues with `CueClose` through the returned command |
| Caption rendering toggle | No cue events |

Replay can emit the same IDs again. Deduplicate by `(VideoID, SourceID,
Cue.TrackID, Cue.ID)` for once-per-source actions. Native automatic loops replay
crossed boundaries across an observed wrap. Browser seek notifications, including
those generated by looping, reconcile destination cues with `CueSeek`. A complete
loop occurring between samples cannot be reconstructed. Delivery is asynchronous,
not a real-time clock or a persistent exactly-once queue. Commands returned by
event handlers may overlap; serialize application work if its order matters.

`Status.CaptionCuesAvailable` indicates that the selected track has a loaded cue
timeline; `Status.CaptionCueErr` reports parsing/limit errors without stopping
playback. Native events support explicitly supplied UTF-8 SRT and WebVTT files
(up to 16 parsed sidecars, 4 MiB and 10,000 cues each). WebVTT timestamp maps are
unsupported. Embedded native captions, ASS, and bitmap tracks still play through
libmpv but do not supply cue timelines. Native text display remains independent.
Browser events use loaded text-track cues; browser access rules still apply.
In-place edits to already loaded browser cues are not tracked; reload the source
to replace its timeline. Both backends sanitize and bound event text.

Try `task video -- -cue-events -captions subtitles.srt movie.mp4` to show the
event count in the demo header. The browser demo accepts `&cue-events=1` and also
logs the events to its developer console. These counts include enters and exits,
including seeks and track changes. Merely enabling events does not enable captions.
Execute the `Close`/`Keep` command to deliver final exits; `Release` alone only
queues them for a later `Update` or `Close`.

`WithPoster(image.Image)` supplies a native fallback image. Browser posters from
Go images are not implemented; the DOM media surface provides its own controls
and status. `WithAutoplay`, `WithLoop`, `WithMuted`, `WithVolume`, `WithHost`, and
`WithZIndex`, and `WithKittyMedium` configure construction. Media format support
remains backend-specific.

## Security considerations

Native media and external captions must be local regular files. URLs, mpv
pseudo-protocols, UNC paths, devices and directories are rejected. libmpv starts
with `config=no`, `load-scripts=no`, `ytdl=no`, no default input bindings or OSC,
no automatic subtitle/audio discovery, and `access-references=no`. FFmpeg
protocol allowlists restrict nested inputs to `file` as well. Browser sources
retain normal browser URL/CORS/CSP behavior; native file limits do not apply to
the DOM backend.

`WithLimits(Limits{...})` bounds native files and source dimensions. Defaults are
2 GiB per file, 4096 per dimension, and 4096×2160 pixels. Headless operations use
the same defaults. Native dimensions are checked when metadata becomes available
and playback is stopped if they exceed limits. Presentation rasters are capped
at 960×540 pixels. These checks are **not a decoder sandbox**: native decoding
can allocate memory before metadata arrives, and files can change after checking.
For strict CPU/memory/filesystem isolation, run decoding in a constrained worker.
Library paths and FFmpeg executable paths must come from trusted host settings.

`WithTranscript` renders a link only for http(s) or relative URLs. The example
player strips terminal escape sequences from the source name and error text it
displays, because the browser build takes the source from the page's query
string. The bundled development server binds to the loopback interface only.

## Validation

```sh
go test -race ./...
go vet ./...
CGO_ENABLED=0 go test ./...
GOOS=js GOARCH=wasm go build ./...
cd examples/player && CGO_ENABLED=0 go build .
```

For native playback integration, set `NTVIDEO_TEST_FILE` to a local clip
at least two seconds long with changing image content, then run
`go test -race . -run 'TestMPV|TestPausedInitialFrame|TestKittyPresentation|TestHeadlessMedia' -v`.
Alternatively, `task test-media SOURCE='/absolute/path/to/movie.mp4'` runs those
checks. Run it for each codec/container you want to validate.

Browser checks use Node and the installed Chrome browser:

```sh
task dev-deps
task test-web
task test-wasm
task test-browser # builds the site and generates its test clip
```

Validation in this checkout: macOS arm64, Go 1.27.1, and libmpv 0.41.0 passed
race tests, vet, decoded-frame/playback/seek/audio-control/visibility integration,
and the native example build. Chrome passed actual WASM playback, Go keyboard
controls, seek, resize, geometry, and cleanup. Linux/amd64 and Windows/amd64
fallback builds and js/wasm builds passed. Linux arm64 (dwarfspark, Go 1.27.3)
passed H.264 and Ogg/Theora playback, pause/seek, paused-frame redraw, Kitty
protocol output, headless extraction, and network-reference rejection three
times each with the race detector, plus pure-Go playback and the full Go race
suite. These automated Kitty checks do not verify a physical terminal display.
Native Windows playback,
audible output quality, real Kitty/tmux display, captions across browsers, and
screen-reader behavior have not been manually verified.

Repository development and validation guidance is in [AGENTS.md](AGENTS.md).
