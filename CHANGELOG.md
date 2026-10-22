# CHANGELOG

## v0.3.0 (2026-10-22)

 * Add optional native `FrameTransform` processing with `WithFrameTransform` and `SetFrameTransform`, including paused redraws and cancellation of stale presentation work.
 * Add caption controls, native terminal-text overlays, and opt-in timestamped cue enter/exit events.
 * Export bounded SRT/WebVTT parsing and headless subtitle transcripts with `ParseCues` and `Cues`; expose subtitle languages, titles, and dispositions in `Probe`.
 * Add size-limited `Frames` batches using one FFmpeg process and sparse seeks. `FrameAt` also accepts output-size limits.
 * Hold native seeks until loading finishes, so hosts can restore positions immediately after `Load`.
 * Discard stale playback samples, give status events independent track slices, and reject invalid timestamps.
 * Report libmpv ABI call failures as errors and publish browser assets only after complete writes.

## v0.2.0 (2026-10-21)

 * Migrate to `ntpicture` with shared terminal sessions and image IDs; update dependencies, including Booba. **API change:** `Model.Close` now returns `tea.Cmd`; use `Release` for media-only teardown.
 * Load libmpv at runtime through goffi, enabling pure-Go builds without mpv development headers. Report an installation hint when libmpv is missing.
 * Add headless `Probe` and `FrameAt`, local-file validation, configurable media limits, and pause-on-hide.
 * Add auto, glyph, and raster rendering modes with the player's **r** toggle; wait for terminal capability detection before switching to raster.
 * Add `registerBoobaHost`, `WithNativeControls(false)`, and browser asset export through `go tool ntvideo-assets DIR [--force]`.
 * Expand browser lifecycle and native playback tests, including H.264 and Ogg validation on Linux ARM64.

## v0.1.0 (2026-10-09)

 * feat(video): Initial release of `ntvideo`, a Bubble Tea v2 video widget. `ntvideo.Model` queues `Play`, `Pause`, `Seek`, `Load`, volume, mute, and track selection on the Tea loop; `Status` and `EventMsg` report state. Browsers (`js/wasm`) play through an HTML video element positioned over the terminal; native builds with `-tags libmpv` play through libmpv and present frames with `ntcharts/picture` as Kitty graphics or half-blocks; other builds report `ErrUnsupported`.
 * feat(video): `FitContain`, `FitCover`, and `FitFill`, `WithScrubber`, captions and audio-track selection, `WithPoster`, and a browser transcript link.
 * feat(video): Native Kitty frames use shared memory by default and fall back to direct PNG when the terminal lacks support. `WithKittyMedium` selects the transport; `Model.Transport()` reports it.
 * feat(examples): `examples/player`, a native and browser player with keyboard and mouse controls, hosted at https://nimbleterminal.github.io/ntvideo/.
 * feat(ci): GitHub Actions for portable, libmpv, and Chrome checks, and a Pages deployment of the browser demo.
