# Migration guide

## Upgrading to v0.3.0

Existing method signatures are unchanged from v0.2.0. New caption, headless,
and frame-transform APIs are optional; see the [README](README.md) for examples.

- Native hosts can opt into `WithFrameTransform` and change it on the Tea loop
  with `SetFrameTransform` (nil disables it). Callbacks run off the UI loop and
  must preserve image immutability. Continue driving `Update` to redraw paused
  frames after changes. Browser playback and headless APIs are unaffected.
- Enable `WithCaptionEvents(true)` to receive `CaptionCueMsg` on the Tea loop.
  Keep forwarding every message to `Update` and running its returned command.
  Native cue events support SRT/WebVTT sidecars; embedded subtitle transcripts
  are available separately through headless `Cues`.
- Use `WithNativeCaptionOverlay(true)` for terminal-text captions over video.
  The default remains mpv-rendered captions. The demo's **t** key toggles this.
- `ParseCues` parses SRT/WebVTT without media tools. Container `Cues` and `Frames`
  use ffprobe/ffmpeg independently of libmpv; bitmap subtitles require OCR and
  are not supported by transcript extraction.
- `Frames` defaults to a 640×360 bounding box, at most 16 frames, and a 16 Mi pixel
  output budget. `FrameAt` keeps source dimensions unless output limits are set,
  but shares the output budget; use `MaxFrameWidth`/`MaxFrameHeight` for large media.
- Native hosts can call `Seek` immediately after `Load` to restore a position.
  The latest early seek wins; loading another source discards it. Old samples
  no longer replace status or emit events after seek/load/caption-track changes.
- Browser hosts should rebuild WASM and refresh assets together with
  `go tool ntvideo-assets dist --force`. The exporter now stages complete files
  before publishing; omitting `--force` still preserves existing files.

## Migrating from v0.1.0 to v0.2.0

### Native hosts

- Replace `ntcharts/picture` integration with `nimbleterminal.dev/ntpicture`.
  Share a terminal session and image-ID allocator with other picture widgets
  using `WithTerminalSession` and `WithImageIDAllocator`.
- `Model.Close()` and `Shutdown()` now return `tea.Cmd`. Return
  `tea.Sequence(v.Close(), tea.Quit)` when quitting. `Release() error` tears
  down media only; it does not clean up terminal images. Use `Keep()` or
  `KeepOnMainScreen()` when retaining the final frame.
- Forward every Tea message to `Update` and run its returned command, including
  late messages after closing. Run commands returned by layout, visibility,
  fit, and render-mode setters.
- Remove the `libmpv` build tag and build-time mpv linking requirements. The
  goffi loader supports `CGO_ENABLED=0`; libmpv is required only for playback.
  Debian/Ubuntu needs `libmpv2`, macOS can use Homebrew's `mpv`, and Windows
  needs a trusted libmpv DLL. `WithLibraryPath` accepts a trusted explicit path.
- Playback starts paused with sound enabled. Hiding pauses playback; showing
  does not resume it. The host owns keys. `WithRenderMode`/`SetRenderMode`
  select auto, glyph, or raster output; the example cycles them with **r**.
- `Probe` and `FrameAt` need ffprobe/ffmpeg, independently of libmpv. Native
  media remains local-file-only with configurable size and dimension limits.

### Browser hosts

Update the host's ntvideo dependency and refresh its browser assets together.
Add this declaration to the host's `go.mod`:

```go
tool nimbleterminal.dev/ntvideo/cmd/ntvideo-assets
```

```sh
go tool ntvideo-assets dist --force
```

After initializing Booba, register it before starting WASM:

```js
import {registerBoobaHost} from './ntvideo/surface.js';
const unregister = registerBoobaHost('terminal', booba);
```

Use `WithHost("terminal")` in Go. Call `unregister()` when disposing the terminal.
The canonical bridge is `ntvideoHost`; `ntchartsVideoHost` remains an alias.
Other terminals can supply their own measurement through `registerHost`.

Use `WithNativeControls(false)` when the terminal owns input, and provide
accessible keyboard controls and a description. The optional terminal scrubber
still supports click-and-drag seeking. Keep bounds aligned with the terminal
layout, and hide video when a modal covers it.

Relative and host-created blob URLs work directly. The host must revoke blob
URLs after closing the video or replacing its source; ntvideo does not revoke
them. Copy the Bubble Tea WASM replacement from the [README](README.md#browser-example)
into the host's module: dependency replacements are not inherited by Go.

## Check the migration

Run `task ci`, then `task ci-native` with libmpv and FFmpeg installed.
Use `task test-media SOURCE='/absolute/path/to/video'` for each local format.
For browser changes, run `task dev-deps` and `task test-browser` with Chrome
installed. See the [README validation notes](README.md#validation) for tested
platforms and checks that still need manual verification.
