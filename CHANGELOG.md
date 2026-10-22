# CHANGELOG

## Unreleased

 * Export bounded SRT/WebVTT parsing and headless container transcript extraction; include subtitle languages, titles, and dispositions in `Probe`.
 * Add size-limited batch frame extraction with one FFmpeg process and sparse input seeks. `FrameAt` also accepts output-size limits.
 * Add opt-in timestamped caption enter/exit events with seek, track-change, load, and close reasons. Support native SRT/WebVTT sidecars and loaded browser text tracks, with bounded parsing and a demo event counter.
 * Add native terminal-text captions over glyph or Kitty video, enabled with `-caption-render text` or the player's **t** key. Expose active native cue text through `Status.CaptionText`.
 * Add caption toggle and track menu to the player, with subtitle-file flags and browser WebVTT query parameters. Opening the menu pauses video without resuming on close.
 * Preserve native external-caption labels and languages; test native and browser caption selection and disabling.

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
