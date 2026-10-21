# ntvideo

**[Try the live browser demo](https://nimbleterminal.github.io/ntvideo/)**

A Bubble Tea v2 video component for Golang Terminal applications. Browsers use a
real HTML video element; native builds load libmpv at runtime for playback and
`ntpicture.Picture` for Kitty graphics or
half-block rendering. Audio belongs to the browser or the OS media backend.

This is an experimental MVP, with a portable public API offering native and browser playback.

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
native controls remain available for accessible media interaction.

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

## Headless metadata and frames

These functions need **ffprobe and ffmpeg**, but no terminal, libmpv, or audio
device. They use local files only, disable network protocols, bound tool output,
and impose a 15-second default deadline (also honoring context cancellation).

```go
info, err := ntvideo.Probe(ctx, "movie.mp4", ntvideo.HeadlessOptions{})
// info: Duration, Width, Height, VideoCodec, AudioCodec, HasAudio
frame, err := ntvideo.FrameAt(ctx, "movie.mp4", time.Second, ntvideo.HeadlessOptions{})
// frame is an owned image.Image, suitable for PNG output, thumbnails or WithPoster.
```

`HeadlessOptions` accepts `Limits`, `Timeout`, and trusted executable paths
`FFprobe`/`FFmpeg`. Missing tools and timestamps at/past EOF return errors. The
viewer does not automatically spawn FFmpeg for a fallback; a host can obtain a
poster with FrameAt and pass it through WithPoster. Without a poster or libmpv,
the widget shows “install mpv to play video”.

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
# macOS; Linux: install libmpv2; Windows: install a trusted libmpv DLL.
brew install mpv
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
source. Browser-native controls provide direct user gestures for playback;
autoplay and programmatic play can be rejected by browser policy.

The build copies go-booba's pinned browser assets from its Go module cache. The
library does not depend on go-booba. The example's separate module does.
Upstream Bubble Tea v2 currently lacks the required js/wasm platform files; this
repository and go-booba use the same fork. **Downstream WASM applications must
include this replacement in their own go.mod**, because Go does not inherit
replacements from dependencies:

```go
replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20261009201241-c397c32a5074
```

Load `web/surface.js` and register a host before initializing the widget. See
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
subtitles are not screen-reader text. There is no synthesized audio description,
transcript extraction, or separate native caption-text stream in this MVP.

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
`go test -race . -run 'TestMPVPlayback|TestHeadlessMedia' -v`.

Browser checks use Node and the installed Chrome browser:

```sh
npm ci
npm test
# After building dist/ and placing a compatible video at dist/demo.mp4:
npm run test:browser
```

Validation in this checkout: macOS arm64, Go 1.27.1, and libmpv 0.41.0 passed
race tests, vet, decoded-frame/playback/seek/audio-control/visibility integration,
and the native example build. Chrome passed actual WASM playback, Go keyboard
controls, seek, resize, geometry, and cleanup. Linux/amd64 and Windows/amd64
fallback builds and js/wasm builds passed. Native Linux/Windows playback,
audible output quality, real Kitty/tmux display, captions across browsers, and
screen-reader behavior have not been manually verified.
