# CHANGELOG

## Unreleased

 * Migrate to `ntpicture.Picture`, shared terminal sessions/ID allocators, asynchronous presentation pacing, and Close/Keep lifecycle commands. `Model.Close` now returns `tea.Cmd`; `Release` handles media-only teardown.
 * Load libmpv at runtime through goffi; remove cgo, build-tag, and development-header requirements. Missing libraries report an installation hint.
 * Add headless `Probe`/`FrameAt` using FFmpeg tools, local-file validation and configurable limits, and pause-on-hide without automatic resume.

## v0.1.0 (2026-10-09)

 * feat(video): Initial release of `ntvideo`, a Bubble Tea v2 video widget. `ntvideo.Model` queues `Play`, `Pause`, `Seek`, `Load`, volume, mute, and track selection on the Tea loop; `Status` and `EventMsg` report state. Browsers (`js/wasm`) play through an HTML video element positioned over the terminal; native builds with `-tags libmpv` play through libmpv and present frames with `ntcharts/picture` as Kitty graphics or half-blocks; other builds report `ErrUnsupported`.
 * feat(video): `FitContain`, `FitCover`, and `FitFill`, `WithScrubber`, captions and audio-track selection, `WithPoster`, and a browser transcript link.
 * feat(video): Native Kitty frames use shared memory by default and fall back to direct PNG when the terminal lacks support. `WithKittyMedium` selects the transport; `Model.Transport()` reports it.
 * feat(examples): `examples/player`, a native and browser player with keyboard and mouse controls, hosted at https://nimbleterminal.github.io/ntvideo/.
 * feat(ci): GitHub Actions for portable, libmpv, and Chrome checks, and a Pages deployment of the browser demo.
