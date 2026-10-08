# ntvideo

**[Try the live browser demo](https://nimbleterminal.github.io/ntvideo/)**

A Bubble Tea v2 video component for Golang Terminal applications. Browsers use a
real HTML video element; native builds optionally use libmpv for playback and
`ntcharts/picture` for Kitty graphics or half-block rendering. Audio belongs to
the browser or the OS media backend.

This is an experimental MVP, with a portable public API offering native and brower playback.

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

The component follows `picture.Model`'s pointer-mutating convention:

- Return `v.Init()` from the parent model's `Init`.
- Forward **every** Bubble Tea message to `v.Update(msg)` and run its returned command.
- Compose `v.View()` into the parent's `tea.View.Content`.
- Call `v.Play()`, `v.Pause()`, `v.Seek(duration)`, `v.SetVolume(0.5)`, or
  `v.SetMuted(true)` on the Tea event loop. They queue work in call order; they
  need no returned command. State is asynchronous: read `v.Status()` or consume
  `ntvideo.EventMsg`, emitted when `Status` changes, using its ID to distinguish widgets.
- Run commands returned by `SetBounds`, `SetSize`, and `SetVisible`. Positions
  are zero-based terminal **cells**, relative to the host's visible grid.
- Before quitting, return `tea.Sequence(v.Shutdown(), tea.Quit)`. Also defer
  `v.Close()` around `Program.Run()` to handle cancellation and errors.

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

## Task runner

The Taskfile follows the sibling ntcharts repositories: `default` builds,
`build-ex-*` writes to `bin/`, and `build-wasm-site` / `serve-wasm-site` manage
the browser example. Run `task list` for all tasks.

```sh
task                                      # library + native libmpv player
task video -- '/path/with spaces/movie.mp4' # build and play (aliases: player, run)
task build-fallback                       # build without libmpv
task test -- -race                        # both Go modules
task test-native -- -race                 # both modules with libmpv
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

Native builds require libmpv and cgo. Browser integration tests require installed
Chrome and FFmpeg with libx264. Build tasks do not automatically update or tidy
module dependencies.

`task ci` fails on formatting, untidy modules, checksum mismatches, whitespace,
race-test failures, vet errors, JavaScript syntax/test failures, or build failures.
It checks both Go modules, vets WASM, builds Linux/Windows fallback players, and
assembles the WASM demo. It requires Go with race-detector support, Git, Node/npm,
and a POSIX shell; it does not require libmpv, FFmpeg, Chrome, or Playwright.
It writes only build outputs/caches and does not run formatting or module fixes.
Use `task fmt` or `task go-tidy` to fix their respective checks.

`task ci-native` adds libmpv builds/vet/race tests and playback tests using a
generated FFmpeg clip. `task ci-full` runs both preflights plus the Chrome
integration suite. Missing dependencies fail explicitly rather than skipping
checks. GitHub Actions runs `task ci` and `task ci-native` on every push and pull
request, runs the Chrome suite in a separate job, and publishes the browser demo
to GitHub Pages from `main` with `task build-pages`.

## Native example

Go 1.26.8 or newer is required.

```sh
# macOS; Linux needs libmpv development headers and pkg-config as well.
brew install mpv pkg-config
cd examples/player
go run -tags libmpv . /path/to/demo.mp4
```

The example starts as a bordered player filling the terminal, with a filename
header and a control panel beneath the video. Click Play/Pause, ±5s, fit, and size
buttons. Volume has clickable −/+ buttons, a draggable slider, a percentage, and
Mute/Unmute. Keyboard controls remain available.

Press Enter for the centered small player with a 60×20 video/scrubber region
(19 video rows and one scrubber row), or Enter again to expand. Borders and
controls add two columns and eight rows. Both layouts shrink with the terminal;
windows smaller than 28×12 show a resize hint. Press space to play. Kitty support is probed by picture; other terminals use
half-blocks. Under tmux, enable passthrough as required by ntcharts/picture.
Native audio plays on the machine running the Go process, including over SSH.

Kitty frames use shared memory by default. picture asks the terminal whether it
can read a shared buffer; when it answers OK, each frame is a raw RGBA copy into
a private object (random name, mode 0600) and only a short command crosses the
tty. That skips a PNG encode of roughly 14 ms per 960×540 frame and hundreds of
kilobytes of stream bandwidth. Terminals without shared memory, remote sessions,
and sandboxes that cannot share a namespace fall back to direct PNG frames
automatically. The player status line shows the active transport, which
`Model.Transport()` also reports (`kitty/shm`, `kitty/png`, `glyph`, `dom`).
`WithKittyMedium(picture.KittyMediumDirect)` forces direct transmission.

`go run . demo.mp4` without the tag builds without any media dependency and shows
a useful unavailable-backend status. `ntvideo.Capabilities()` reports compiled and
environment support. Codec, media, audio-device, and permission failures remain
runtime errors in `Status.Err`.

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
replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20260928192001-1b36865b418a
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

Media sources are opened by the platform backend, so they carry that backend's
trust model. Browsers apply CORS, CSP, and media policies. libmpv accepts local
paths, HTTP(S), and other schemes of its own (`edl://`, `lavf://`, `fd://`, and
more), so **native sources and caption paths must come from trusted input**, or
the application must allowlist schemes before calling `WithSource`, `WithMedia`,
or `Load`. The native backend disables mpv's user configuration, Lua scripts, and
the youtube-dl hook, so a URL never spawns an external program.

`WithTranscript` renders a link only for http(s) or relative URLs. The example
player strips terminal escape sequences from the source name and error text it
displays, because the browser build takes the source from the page's query
string. The bundled development server binds to the loopback interface only.

## Validation

```sh
go test -race ./...
go vet ./...
go test -tags libmpv -race ./...
GOOS=js GOARCH=wasm go build ./...
cd examples/player && go build -tags libmpv .
```

For native playback integration, set `NTVIDEO_TEST_FILE` to a local clip
at least two seconds long with changing image content, then run
`go test -tags libmpv -race . -run TestMPVPlayback -v`.

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
