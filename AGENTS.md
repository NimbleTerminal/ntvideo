# Working on ntvideo

ntvideo is a Bubble Tea v2 component with native runtime-loaded libmpv and
browser HTML video backends. Read README.md for the public contract and
MIGRATING.md for v0.3.0 upgrade notes and earlier compatibility changes.

## Structure and boundaries

- The root Go module is the library; `examples/player` is a separate module
  with a local replacement pointing at the root. Keep both modules consistent.
- Native media uses goffi, not cgo or purego. Preserve pure-Go builds and
  graceful startup when libmpv is missing. Keep native sources local-only and
  retain decoder configuration restrictions and resource limits.
- Use ntpicture for native presentation, shared terminal sessions, image IDs,
  and Close/Keep cleanup. Preserve asynchronous presentation pacing and stale
  frame rejection when seeking, resizing, hiding, or switching modes.
- Hosts own keys, focus, bounds, and visibility. Playback starts paused;
  showing a hidden widget must not resume playback automatically.
- Browser geometry and host registration live in `web/surface.js`. The root
  library and asset command must not acquire a Booba dependency; the example
  owns that integration. Embed runtime assets only, not examples or tests.
- Browser blob URLs belong to the host. Preserve native-control defaults and
  accessible host-owned input when `WithNativeControls(false)` is selected.
- Caption cue events are opt-in and independent of raster/text presentation.
  Keep source generations, stale-sample rejection, overlapping/short cues, and
  seek/track/close reasons intact. Parsing errors must not stop video playback.
- Headless transcript/frame APIs share local-file, deadline, and output limits.
  Batch frames must seek to requested times rather than decode intervening spans;
  preserve request order, independent duplicate images, and all-or-error results.

## Validation

- `task ci` checks formatting, both modules, race tests, vet, Node/WASM tests,
  and portable builds. It requires Go, Git, Node/npm, and a POSIX shell.
- `task ci-native` adds generated-media playback checks; install libmpv and
  FFmpeg first. Debian/Ubuntu needs `libmpv2`, not just the `mpv` executable.
- `task test-media SOURCE='/absolute/path/to/video'` exercises native playback,
  presentation, headless tools, and input restrictions with the race detector.
  Plain Go tests skip media integration unless `NTVIDEO_TEST_FILE` is set.
- Backend integration tests must keep polling events while awaiting frames,
  including asynchronous seek completion, just as the host update loop does.
- For browser changes, run `task test-web`, `task test-wasm`, and
  `task test-browser`. Install test dependencies with `task dev-deps`; the
  browser integration suite requires Chrome and FFmpeg.
- Distinguish protocol-output tests from actual Kitty/tmux visual inspection,
  and automated audio state checks from listening to audio output.

## Documentation and releases

Keep README.md, MIGRATING.md, and CHANGELOG.md aligned with public API changes.
Use a short dated changelog entry, calling out breaking signatures explicitly.
The release version is recorded in the changelog and Git tag; there is no
separate version constant to bump. Do not tag or publish merely to edit release
documentation. Browser consumers must re-export assets when upgrading ntvideo.
